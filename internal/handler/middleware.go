package handler

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/m-j-majevsky/url-shortener/internal/auth"
	"go.uber.org/zap"
)

func responseContentTypeMiddleware(next http.Handler, ct string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ContentType, ct)
		next.ServeHTTP(w, r)
	})
}

// compressWriter реализует интерфейс http.ResponseWriter и позволяет прозрачно для сервера
// сжимать передаваемые данные и выставлять правильные HTTP-заголовки
type compressWriter struct {
	w           http.ResponseWriter
	zw          *gzip.Writer
	wroteHeader bool
	gzipOn      bool // gzip применён: тело пишется в zw и объявлено заголовком Content-Encoding
}

func newCompressWriter(w http.ResponseWriter) *compressWriter {
	return &compressWriter{
		w:  w,
		zw: gzip.NewWriter(w),
	}
}

func (c *compressWriter) Header() http.Header {
	return c.w.Header()
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}

	// Тело пишем в gzip.Writer только если заголовок Content-Encoding выставлен;
	// иначе — напрямую в оригинальный writer: ответ остаётся несжатым.
	if c.gzipOn {
		return c.zw.Write(p)
	}
	return c.w.Write(p)
}

func (c *compressWriter) WriteHeader(statusCode int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true

	// Gzip применяется только к успешным ответам с телом (< 300).
	// Для ответов без тела (204 No Content, 304 Not Modified) gzip не применяется:
	// закрытие gzip.Writer записало бы в пустой ответ gzip-заголовок и футер.
	// Тела ошибок (>= 300) отправляются несжатыми: без заголовка Content-Encoding
	// клиент читает текст ошибки напрямую, без распаковки.
	if statusCode != http.StatusNoContent && statusCode != http.StatusNotModified && statusCode < 300 {
		c.gzipOn = true
		c.w.Header().Set("Content-Encoding", "gzip")
	}

	c.w.WriteHeader(statusCode)
}

// Close закрывает gzip.Writer и досылает все данные из буфера,
// если gzip был применён к ответу.
func (c *compressWriter) Close() error {
	if !c.gzipOn {
		// Gzip не применялся (ответ без тела или ошибка): Close здесь
		// записал бы в w пустой gzip-поток (заголовок и футер),
		// которого в ответе быть не должно.
		return nil
	}
	return c.zw.Close()
}

// compressReader реализует интерфейс io.ReadCloser и позволяет прозрачно для сервера
// декомпрессировать получаемые от клиента данные
type compressReader struct {
	r  io.ReadCloser
	zr *gzip.Reader
}

func newCompressReader(r io.ReadCloser) (*compressReader, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}

	return &compressReader{
		r:  r,
		zr: zr,
	}, nil
}

func (c *compressReader) Read(p []byte) (n int, err error) {
	return c.zr.Read(p)
}

func (c *compressReader) Close() error {
	if err := c.r.Close(); err != nil {
		return err
	}
	return c.zr.Close()
}

func GzipMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// middleware используется только для разрешенных значений Content-Type запроса
		if !isValidRequestContentType(r.Header.Get(ContentType)) {
			h.ServeHTTP(w, r)
			return
		}

		// по умолчанию устанавливаем оригинальный http.ResponseWriter как тот,
		// который будем передавать следующей функции
		ow := w

		// проверяем, что клиент умеет получать от сервера сжатые данные в формате gzip
		acceptEncoding := r.Header.Get("Accept-Encoding")
		supportsGzip := strings.Contains(acceptEncoding, "gzip")

		if supportsGzip {
			// оборачиваем оригинальный http.ResponseWriter новым с поддержкой сжатия
			cw := newCompressWriter(w)
			// меняем оригинальный http.ResponseWriter на новый
			ow = cw
			// не забываем отправить клиенту все сжатые данные после завершения middleware
			defer cw.Close()
		}

		// проверяем, что клиент отправил серверу сжатые данные в формате gzip
		contentEncoding := r.Header.Get("Content-Encoding")
		sendsGzip := strings.Contains(contentEncoding, "gzip")
		if sendsGzip {
			// оборачиваем тело запроса в io.Reader с поддержкой декомпрессии
			cr, err := newCompressReader(r.Body)
			if err != nil {
				// Невалидное gzip-тело — ошибка клиента, а не сервера.
				http.Error(w, "тело запроса не является корректным gzip-потоком", http.StatusBadRequest)
				return
			}
			// меняем тело запроса на новое
			r.Body = cr
			defer cr.Close()
		}

		// передаём управление хендлеру
		h.ServeHTTP(ow, r)
	})
}

func isValidRequestContentType(ct string) bool {
	return ct == TextPlain || ct == AppJSON
}

// Функциональность установки-извлечения cookie с ID пользователя

func CookieMiddleware(log *zap.Logger, users UserRegistrator, params UserCookieParams) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var userID string
			var setNewCookie bool

			cookie, err := r.Cookie(params.UserCookieName)
			if err != nil {
				// Cookie не найдена; требуется выдача токена
				setNewCookie = true
			} else { // err == nil
				var parsingErr error
				userID, parsingErr = auth.ParseUserIDJWT(cookie.Value, params.SigningKey)
				if parsingErr != nil {
					// Ошибки парсинга и дешифровки воспринимаем как указание на невалидность токена,
					// что требует выдачи нового
					setNewCookie = true
				} else { // parsingErr == nil
					if userID == "" {
						// Если кука присутствует в запросе, но не содержит ID пользователя,
						// так как она пуста, то возвращаем HTTP-статус 401 Unauthorized
						w.WriteHeader(http.StatusUnauthorized)
						return
					}

					// setNewCookie остался false, userID не пуст
				}
			}

			if setNewCookie {
				userID, err = users.CreateUser(r.Context())
				if err != nil {
					log.Error("user creating failed", zap.Error(err))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				responseCookieValue, err := auth.GenerateUserIDJWT(userID, params.UserCookieTTL, params.SigningKey)
				if err != nil {
					log.Error("JWT encrypting failed", zap.Error(err))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				http.SetCookie(w, &http.Cookie{
					Name:     params.UserCookieName,
					Value:    responseCookieValue,
					Path:     "/",
					Expires:  time.Now().Add(params.UserCookieTTL),
					HttpOnly: true,         // Устанавливаем HttpOnly как рекомендацию по безопасности
					Secure:   r.TLS != nil, // Устанавливаем Secure только для HTTPS
					SameSite: http.SameSiteStrictMode,
				})
			} else { // setNewCookie == false
				// userID содержит непустое значение. Проверим его
				found, err := users.CheckUserExists(r.Context(), userID)
				if err != nil {
					log.Error("user check in storage failed", zap.Error(err))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if !found {
					// Если кука присутствует в запросе, но не содержит ID пользователя,
					// то возвращаем HTTP-статус 401 Unauthorized
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
			}

			ctx := context.WithValue(r.Context(), userIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

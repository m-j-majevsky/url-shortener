package handler

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
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

	return c.zw.Write(p)
}

func (c *compressWriter) WriteHeader(statusCode int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true

	if statusCode < 300 {
		c.w.Header().Set("Content-Encoding", "gzip")
	}

	c.w.WriteHeader(statusCode)
}

// Close закрывает gzip.Writer и досылает все данные из буфера.
func (c *compressWriter) Close() error {
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
				w.WriteHeader(http.StatusInternalServerError)
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

type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
}

func CookieMiddleware(storage UserStorage, params UserCookieParams) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var userID string
			var setNewCookie bool

			cookie, err := r.Cookie(params.UserCookieName)
			if err != nil {
				// Cookie не найдена; требуется выдача токена
				setNewCookie = true
			} else { // err == nil
				var decryptingErr error
				userID, decryptingErr = decryptAndParseJWT(cookie.Value, params.EncryptingKey, params.SigningKey)
				if decryptingErr != nil {
					// Ошибки парсинга и дешифровки воспринимаем как указание на невалидность токена,
					// что требует выдачи нового
					setNewCookie = true
				} else { // decryptingErr == nil
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
				userID, err = storage.CreateUser(r.Context())
				if err != nil {
					logger.Log.Error("user creating failed", zap.Error(err))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				responseCookieValue, err := generateEncryptedJWT(userID, params.UserCookieTTL, params.EncryptingKey, params.SigningKey)
				if err != nil {
					logger.Log.Error("JWT encrypting failed", zap.Error(err))
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
				found, err := storage.CheckUserExists(r.Context(), userID)
				if err != nil {
					logger.Log.Error("user check in storage failed", zap.Error(err))
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

			ctx := context.WithValue(r.Context(), params.userIDKey, userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// router_gzip_test.go — тесты семантики gzip-миддлвэра с пулом писателей
// (sync.Pool). Дополняют TestGzipCompression из router_test.go
// кейсами, которых там нет: отказ от сжатия ошибок и 204, переиспользование
// писателей из пула и конкурентная работа пула (гонится под -race).
//
// Все тесты ходят сырым http-клиентом: resty сам добавляет Accept-Encoding
// и декомпрессирует ответы — с ним заголовки сжатия не проверить.
package handler_test

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/m-j-majevsky/url-shortener/internal/handler"
)

// Параметры конкурентного стресс-теста пула.
const (
	gzipPoolWorkers       = 8
	gzipPoolReqsPerWorker = 25
)

// gzipProbe — результат probeGzip: сырой ответ и прочитанное тело
// (сжатое — распакованное, несжатое — как есть).
type gzipProbe struct {
	resp *http.Response
	body string
}

// probeGzip выполняет HTTP-запрос с полным контролем заголовков сжатия
// и возвращает ответ вместе с телом: если Content-Encoding: gzip —
// тело распаковывается. Accept-Encoding выставляется всегда явно:
// пустой — чтобы транспорт не добавил gzip сам и не декомпрессировал ответ.
//
// Функция не использует require и безопасна для вызова из горутин
// (см. TestGzipPoolConcurrent).
func probeGzip(client *http.Client, method, url, reqCT, body string, acceptGzip bool) (*gzipProbe, error) {
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		return nil, fmt.Errorf("создание запроса: %w", err)
	}
	if reqCT != "" {
		req.Header.Set(handler.ContentType, reqCT)
	}
	if acceptGzip {
		req.Header.Set("Accept-Encoding", "gzip")
	} else {
		req.Header.Set("Accept-Encoding", "")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("выполнение запроса: %w", err)
	}
	defer resp.Body.Close()

	p := &gzipProbe{resp: resp}
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("создание gzip-ридера: %w", err)
		}
		defer zr.Close()
		raw, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("чтение сжатого тела: %w", err)
		}
		p.body = string(raw)
	} else {
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("чтение тела: %w", err)
		}
		p.body = string(raw)
	}
	return p, nil
}

// TestGzipNoCompressionOnConflict — 409 не сжимается: тело ошибки уходит
// несжатым (statusCode >= 300 => gzipOn = false), читается как обычный текст.
// С запросом, просящим сжатие: писатель из пула на этом пути не извлекается.
func (s *RouterTestSuite) TestGzipNoCompressionOnConflict() {
	ts, _ := s.newRealRouterServer(nil)

	p, err := probeGzip(http.DefaultClient, http.MethodPost, ts.URL+"/", handler.TextPlain, yandexLongURL, true)
	s.Require().NoError(err)
	s.Equal(http.StatusConflict, p.resp.StatusCode)
	s.NotEqual("gzip", p.resp.Header.Get("Content-Encoding"), "тело ошибки уходит несжатым")
	s.Equal(targetBaseURL+"/"+yandexToken, p.body, "тело конфликта читается как обычный текст")
}

// TestGzipNoCompressionOnNoContent — 204 не сжимается: закрытие gzip.Writer
// не должно вписывать заголовок и футер gzip в пустой ответ.
//
// Нюанс: GzipMiddleware оборачивает writer только для запросов с Content-Type
// text/plain или application/json. Обычный GET без Content-Type минует
// gzip-слой целиком и кейса 204 не проверяет, поэтому GET несёт явный
// Content-Type — он проходит через compressWriter, а хендлеру он безразличен.
func (s *RouterTestSuite) TestGzipNoCompressionOnNoContent() {
	ts, _ := s.newRealRouterServer(nil)

	// Свежий пользователь: пустой список URL => 204
	jar, err := cookiejar.New(nil)
	s.Require().NoError(err)
	cli := &http.Client{Jar: jar, Timeout: 10 * time.Second}

	p, err := probeGzip(cli, http.MethodGet, ts.URL+"/api/user/urls", handler.AppJSON, "", true)
	s.Require().NoError(err)
	s.Equal(http.StatusNoContent, p.resp.StatusCode)
	s.NotEqual("gzip", p.resp.Header.Get("Content-Encoding"), "204 уходит несжатым")
	s.Empty(p.body, "у 204 нет тела — и нет gzip-заголовка/футера")
}

// TestGzipPoolReuse — последовательность gzip -> 409 -> gzip: писатель,
// возвращённый в пул первым ответом, переиспользуется последним.
// Ловит главную регрессию пула — забытый Reset при извлечении: тогда тело
// уходит в чужой ResponseWriter и ответ оказывается пустым или битым.
// Повторный запрос после 409 дополнительно проверяет, что путь без сжатия
// не повредил состояние пула.
func (s *RouterTestSuite) TestGzipPoolReuse() {
	ts, _ := s.newRealRouterServer(nil)

	// 1) Первый сжатый ответ: New() или извлечение из пула — неотличимо снаружи.
	p1, err := probeGzip(http.DefaultClient, http.MethodPost, ts.URL+"/", handler.TextPlain, "https://pool.reuse.one.io", true)
	s.Require().NoError(err)
	s.Equal(http.StatusCreated, p1.resp.StatusCode)
	s.Equal("gzip", p1.resp.Header.Get("Content-Encoding"))
	s.True(strings.HasPrefix(p1.body, targetBaseURL+"/"), "неожиданное тело первого ответа: %q", p1.body)
	s.NotEqual(targetBaseURL+"/", p1.body, "токен сгенерирован")

	// 2) Между ними — ошибка: на этом пути писатель не извлекался и не возвращался.
	p2, err := probeGzip(http.DefaultClient, http.MethodPost, ts.URL+"/", handler.TextPlain, yandexLongURL, true)
	s.Require().NoError(err)
	s.Equal(http.StatusConflict, p2.resp.StatusCode)
	s.NotEqual("gzip", p2.resp.Header.Get("Content-Encoding"))
	s.Equal(targetBaseURL+"/"+yandexToken, p2.body, "тело конфликта читается как обычный текст")

	// 3) Серия сжатых ответов после ошибки: каждый декомпрессируется в свой
	// короткий URL (Reset(c.w) перецепил писатель на новый ответ).
	seen := make(map[string]bool)
	for i, longURL := range []string{"https://pool.reuse.two.io", "https://pool.reuse.three.io", "https://pool.reuse.four.io"} {
		p, err := probeGzip(http.DefaultClient, http.MethodPost, ts.URL+"/", handler.TextPlain, longURL, true)
		s.Require().NoError(err)
		s.Equal(http.StatusCreated, p.resp.StatusCode, "сжатый ответ %d", i)
		s.Equal("gzip", p.resp.Header.Get("Content-Encoding"), "сжатый ответ %d", i)
		s.True(strings.HasPrefix(p.body, targetBaseURL+"/"),
			"сжатый ответ %d не декомпрессировался в ожидаемый короткий URL: %q", i, p.body)
		s.False(seen[p.body], "сжатый ответ %d повторяет чужое тело: %q", i, p.body)
		seen[p.body] = true
	}
}

// TestGzipPoolConcurrent — стресс-тест расщепления состояния пула:
// 8 горутин x 25 сжатых ответов. Проверяет, что при конкуренции каждый ответ
// декомпрессируется в свой короткий URL и несёт Content-Encoding: gzip.
// Гонится под -race (go test -race ./internal/handler).
func (s *RouterTestSuite) TestGzipPoolConcurrent() {
	ts, _ := s.newRealRouterServer(nil)

	cli := &http.Client{Timeout: 10 * time.Second}
	var counter int64
	var wg sync.WaitGroup
	for w := 0; w < gzipPoolWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < gzipPoolReqsPerWorker; i++ {
				n := atomic.AddInt64(&counter, 1)
				longURL := fmt.Sprintf("https://pool.concurrent/%d", n)

				p, err := probeGzip(cli, http.MethodPost, ts.URL+"/", handler.TextPlain, longURL, true)
				if err != nil {
					s.Errorf(err, "запрос %d: ошибка обмена", n)
					continue
				}
				if p.resp.StatusCode != http.StatusCreated {
					s.Errorf(fmt.Errorf("получен код %d", p.resp.StatusCode), "запрос %d: ожидался 201", n)
					continue
				}
				if encHeader := p.resp.Header.Get("Content-Encoding"); encHeader != "gzip" {
					s.Errorf(fmt.Errorf("получен заголовок %q", encHeader), "запрос %d: ожидался Content-Encoding: gzip", n)
					continue
				}
				if !strings.HasPrefix(p.body, targetBaseURL+"/") || p.body == targetBaseURL+"/" {
					s.Errorf(fmt.Errorf("тело %q", p.body), "запрос %d: не декомпрессировался в ожидаемый короткий URL", n)
				}
			}
		}()
	}
	wg.Wait()
}

package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// benchBody — ~1 КБ JSON, типичный ответ /api/user/urls.
// Формируется один раз на пакетном уровне, чтобы не было
// аллокаций на построение тела внутри замера.
var benchBody = buildBenchBody()

func buildBenchBody() []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"urls":[`)
	for i := 0; i < 16; i++ {
		buf.WriteString(`{"short_url":"http://bench.example.com/AbCdEf","original_url":"https://bench.example.com/some/long/url/000000000"}`)
		if i < 15 {
			buf.WriteByte(',')
		}
	}
	buf.WriteString(`]}`)
	return buf.Bytes()
}

// benchTargetHandler — простейший тестовый хендлер: пишет фиксированный JSON.
// Content-Type application/json обязателен: GzipMiddleware сжимает
// только успешные ответы с телом и только для поддерживаемых типов запроса.
func benchTargetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ContentType, AppJSON)
		_, _ = w.Write(benchBody)
	})
}

func newBenchRequest(path string, gzipped bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(ContentType, AppJSON)
	if gzipped {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	return req
}

// devNullLogger — прод-подобный JSON-логгер, записи уходят в io.Discard:
// механика логирования замеряется полностью, вывод бенчмарка не засоряется.
func devNullLogger() *zap.Logger {
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(io.Discard), zapcore.InfoLevel)
	return zap.New(core)
}

// BenchmarkGzipMiddleware — цена сжатия против отсутствия сжатия.
// Базовая линия для решения по sync.Pool для gzip.NewWriter:
// сейчас compressWriter создаёт новый писатель на каждый сжимаемый ответ.
func BenchmarkGzipMiddleware(b *testing.B) {
	h := GzipMiddleware(benchTargetHandler())

	for _, tc := range []struct {
		name    string
		gzipped bool
	}{
		{"no_gzip", false},
		{"gzip", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			req := newBenchRequest("/api/user/urls", tc.gzipped)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				if rr.Body.Len() == 0 {
					b.Fatal("пустой ответ")
				}
			}
		})
	}

	runtime.GC()
}

// BenchmarkLoggingMiddleware — цена middleware логирования на каждый запрос:
// обёртка loggingResponseWriter плюс две записи log.Info с полями zap
// (uri/method/duration и status/size).
func BenchmarkLoggingMiddleware(b *testing.B) {
	// nop: чистая механика middleware без записей.
	nopHandler := logger.WithLogging(zap.NewNop())

	// в /dev/null: ближе к прод-поведению — записи реально формируются.
	devHandler := logger.WithLogging(devNullLogger())

	for _, tc := range []struct {
		name   string
		middle http.Handler
	}{
		{"nop", nopHandler(benchTargetHandler())},
		{"dev_null", devHandler(benchTargetHandler())},
	} {
		b.Run(tc.name, func(b *testing.B) {
			req := newBenchRequest("/api/user/urls", false)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				rr := httptest.NewRecorder()
				tc.middle.ServeHTTP(rr, req)
				if rr.Body.Len() == 0 {
					b.Fatal("пустой ответ")
				}
			}
		})
	}

	runtime.GC()
}

// BenchmarkFullChain — полная цепочка как в проде:
// WithLogging обёрнута снаружи GzipMiddleware (порядок Use в NewRouter).
// Итоговая цена middleware-слоя, который несёт каждый запрос.
func BenchmarkFullChain(b *testing.B) {
	h := logger.WithLogging(zap.NewNop())(GzipMiddleware(benchTargetHandler()))

	req := newBenchRequest("/api/user/urls", true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Body.Len() == 0 {
			b.Fatal("пустой ответ")
		}
	}

	runtime.GC()
}

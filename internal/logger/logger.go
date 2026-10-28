package logger

import (
	"net/http"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Initialize создаёт логер с необходимым уровнем логирования и возвращает его.
func Initialize(level string) (*zap.Logger, error) {
	// преобразуем текстовый уровень логирования в zap.AtomicLevel
	lvl, err := zap.ParseAtomicLevel(level)
	if err != nil {
		return nil, err
	}
	// создаём новую конфигурацию логера
	cfg := zap.NewProductionConfig()
	// устанавливаем уровень
	cfg.Level = lvl
	// настраиваем привычный формат логирования времени в UTC
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	// создаём логер на основе конфигурации
	zl, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return zl, nil
}

// Функционал логирования ответа от сервера
type (
	// берём структуру для хранения сведений об ответе
	responseData struct {
		status int
		size   int
	}

	// добавляем реализацию http.ResponseWriter
	loggingResponseWriter struct {
		http.ResponseWriter // встраиваем оригинальный http.ResponseWriter
		responseData        *responseData
	}
)

func (r *loggingResponseWriter) Write(b []byte) (int, error) {
	// записываем ответ, используя оригинальный http.ResponseWriter
	size, err := r.ResponseWriter.Write(b)
	r.responseData.size += size // захватываем размер
	return size, err
}

func (r *loggingResponseWriter) WriteHeader(statusCode int) {
	// записываем код статуса, используя оригинальный http.ResponseWriter
	r.ResponseWriter.WriteHeader(statusCode)
	r.responseData.status = statusCode // захватываем код статуса
}

// WithLogging — middleware, логирующая входящие HTTP-запросы и ответы сервера.
// Для каждого запроса выводит URI, метод, длительность обработки,
// а затем код статуса и размер ответа.
func WithLogging(log *zap.Logger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		logFn := func(w http.ResponseWriter, r *http.Request) {
			responseData := &responseData{
				status: 0,
				size:   0,
			}
			lw := loggingResponseWriter{
				ResponseWriter: w, // встраиваем оригинальный http.ResponseWriter
				responseData:   responseData,
			}

			start := time.Now()
			h.ServeHTTP(&lw, r) // внедряем реализацию http.ResponseWriter
			duration := time.Since(start)

			// отправляем сведения о запросе в zap
			log.Info("входящий запрос",
				zap.String("uri", r.RequestURI),
				zap.String("method", r.Method),
				zap.Duration("duration", duration),
			)
			// следом отправляем в zap сведения об ответе сервера
			log.Info("ответ сервера",
				zap.Int("status", responseData.status), // получаем перехваченный код статуса ответа
				zap.Int("size", responseData.size),     // получаем перехваченный размер ответа
			)
		}
		return http.HandlerFunc(logFn)
	}
}

package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

// ApplicationConfig содержит все параметры работы сервиса.
type ApplicationConfig struct {
	LogLevel          string        // уровень логирования ("debug", "info", ...)
	ServerRunAddress  string        // адрес и порт запуска HTTP-сервера
	TargetBaseURL     string        // базовый URL сокращённых ссылок
	FileStoragePath   string        // путь к файлу сохранения состояния; пусто — файловое хранилище не используется
	DatabaseDSN       string        // строка подключения к PostgreSQL; пусто — PostgreSQL не используется
	CookieUserIDName  string        // имя cookie, в котором хранится JWT с ID пользователя, выданным клиенту
	SigningKey        []byte        // ключ подписания JWT
	CookieUserIDTTL   time.Duration // время жизни cookie, имя которого хранится в CookieUserIDName
	SaveStateInterval time.Duration // резерв: интервал для таймера сохранения данных в FileStoragePath; сейчас не потребляется
	ShutdownTimeout   time.Duration // время на graceful shutdown
	AuditFile         string        // путь к файлу-приёмнику аудита; пусто — аудит в файл отключён
	AuditURL          string        // URL удалённого приёмника аудита; пусто — аудит на удалённый сервер отключён
}

// LoadApplicationConfig загружает конфигурацию из переменных окружения
// и флагов командной строки и проверяет, что обязательные параметры заданы.
//
// Конфигурация разделена на два слоя:
//   - readApplicationConfig — слой чтения: считывает значения из env и флагов,
//     не валидируя их содержимое (единственное исключение — параметр
//     SAVE_STATE_INTERVAL, требующий интерпретации как time.Duration);
//   - validateApplicationConfig — слой валидации: проверяет, что обязательные
//     параметры непусты, и возвращает итоговую структуру или ошибку.
//
// Такое разделение позволяет менять логику валидации, не затрагивая слой чтения,
// и наоборот — например, если в будущем пустое значение станет допустимым
// для какого-то параметра, изменится только валидация.
func LoadApplicationConfig() (ApplicationConfig, error) {
	cfg, err := readApplicationConfig()
	if err != nil {
		return cfg, err
	}
	return validateApplicationConfig(cfg)
}

// envOr возвращает значение переменной окружения, если она задана
// и не пуста, иначе — fallback.
func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// readApplicationConfig — слой чтения: считывает значения из переменных
// окружения и флагов командной строки. Приоритет источников (от низшего
// к высшему):
//  1. жёстко заданные значения по умолчанию;
//  2. флаги командной строки — парсятся со своими дефолтами;
//  3. переменные окружения — перекрывают флаги.
//
// Реализация: флаги парсятся с жёсткими дефолтами, затем каждая переменная
// окружения, если задана и непуста, перекрывает полученное значение.
// Слой не валидирует значения — пустая строка считается валидным
// результатом чтения.
func readApplicationConfig() (ApplicationConfig, error) {
	cfg := ApplicationConfig{}

	// Шаг 1: жёстко заданные значения по умолчанию — кладутся в дефолты флагов.
	const (
		defaultLogLevel  = "info"
		defaultRunAddr   = ":8080"
		defaultBaseURL   = "http://localhost:8080"
		defaultCookieNm  = "user_id_jwt"
		defaultSaveState = time.Second // дефолт флага -s
	)

	// Шаг 2: парсим флаги с жёсткими дефолтами.
	flag.StringVar(&cfg.ServerRunAddress, "a", defaultRunAddr, "address and port to run server")
	flag.StringVar(&cfg.TargetBaseURL, "b", defaultBaseURL, "target URL base path")
	flag.StringVar(&cfg.LogLevel, "l", defaultLogLevel, "log level")
	flag.StringVar(&cfg.DatabaseDSN, "d", "", "data source name")
	flag.StringVar(&cfg.FileStoragePath, "f", "", "path to storage saved state")
	flag.StringVar(&cfg.CookieUserIDName, "u", defaultCookieNm, "user ID cookie name")
	// Резерв: параметр не потребляется кодом (состояние сохраняется
	// через defer saveStateDeferred в main), но флаг сохраняем, чтобы
	// flag.Parse не падал, если платформа передаёт -s.
	flag.DurationVar(&cfg.SaveStateInterval, "s", defaultSaveState, "save state interval (reserved, not consumed)")
	flag.StringVar(&cfg.AuditFile, "audit-file", "", "path to file audit sink; empty value disables file audit")
	flag.StringVar(&cfg.AuditURL, "audit-url", "", "URL of remote audit sink; empty value disables remote audit")
	flag.Parse()

	// Шаг 3: переменные окружения перекрывают флаги.
	cfg.LogLevel = envOr("LOG_LEVEL", cfg.LogLevel)
	cfg.ServerRunAddress = envOr("SERVER_ADDRESS", cfg.ServerRunAddress)
	cfg.TargetBaseURL = envOr("BASE_URL", cfg.TargetBaseURL)
	cfg.DatabaseDSN = envOr("DATABASE_DSN", cfg.DatabaseDSN)
	cfg.FileStoragePath = envOr("FILE_STORAGE_PATH", cfg.FileStoragePath)
	cfg.CookieUserIDName = envOr("COOKIE_USER_ID", cfg.CookieUserIDName)
	cfg.AuditFile = envOr("AUDIT_FILE", cfg.AuditFile)
	cfg.AuditURL = envOr("AUDIT_URL", cfg.AuditURL)

	// Ключ подписи JWT: только из env, флагом не параметризуется.
	// Валидация требует непустого значения; хардкод по умолчанию
	// подставляется в cmd/shortener при ErrEmptySigningKey.
	cfg.SigningKey = []byte(envOr("SIGNING_KEY", ""))

	// SAVE_STATE_INTERVAL — единственный параметр, требующий интерпретации
	// на слое чтения (time.ParseDuration), поэтому read-слой здесь
	// возвращает ошибку. В gophermart read-слой ошибок не возвращает.
	if envTickPeriod := envOr("SAVE_STATE_INTERVAL", ""); envTickPeriod != "" {
		ssp, err := time.ParseDuration(envTickPeriod)
		if err != nil {
			return cfg, fmt.Errorf("некорректное значение SAVE_STATE_INTERVAL %q: %w", envTickPeriod, err)
		}
		cfg.SaveStateInterval = ssp
	}

	// Значения по умолчанию, не параметризуемые извне
	cfg.ShutdownTimeout = 10 * time.Second
	cfg.CookieUserIDTTL = 24 * time.Hour

	return cfg, nil
}

// sentinel-значение вводится для обработки отсутствия переменной окружения
// в тестах (GitHub Actions) платформы Практикума Яндекс.
var ErrEmptySigningKey = errors.New("SIGNING_KEY не задан (используйте переменную окружения SIGNING_KEY)")

// validateApplicationConfig — слой валидации: проверяет, что обязательные
// параметры заданы и непусты. Возвращает конфигурацию без изменений
// или ошибку с описанием недостающего параметра.
func validateApplicationConfig(cfg ApplicationConfig) (ApplicationConfig, error) {
	if cfg.ServerRunAddress == "" {
		return cfg, fmt.Errorf("SERVER_ADDRESS не задан (используйте флаг -a или переменную окружения SERVER_ADDRESS)")
	}
	if cfg.TargetBaseURL == "" {
		return cfg, fmt.Errorf("BASE_URL не задан (используйте флаг -b или переменную окружения BASE_URL)")
	}
	if cfg.LogLevel == "" {
		return cfg, fmt.Errorf("LOG_LEVEL не задан (используйте флаг -l или переменную окружения LOG_LEVEL)")
	}
	if len(cfg.SigningKey) == 0 {
		return cfg, ErrEmptySigningKey
	}

	return cfg, nil
}

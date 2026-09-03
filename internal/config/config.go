package config

import (
	"flag"
	"os"
	"time"

	"github.com/m-j-majevsky/url-shortener/internal/service"
)

type ApplicationConfig struct {
	LogLevel          string
	ServerRunAddress  string
	TargetBaseURL     string
	FileStoragePath   string
	DatabaseDSN       string
	CookieUserIDName  string        // имя cookie, в котором будет хранится JWT с ID пользователя, выданным клиенту
	SigningKey        []byte        // ключ подписания JWT
	EncryptingKey     []byte        // 32-байтовый ключ шифра AES
	CookieUserIDTTL   time.Duration // время жизни cookie, имя которого хранится в CookieUserIDName
	SaveStateInterval time.Duration // интервал для таймера на сохранение данных в FileStoragePath
	ShutdownTimeout   time.Duration // время на graceful shutdown
	ServiceConfig     service.ShortenerConfig
}

func LoadApplicationConfig() (ApplicationConfig, error) {
	svcConfig := service.DefaultShortenerConfig()

	// Имя файла для логирования необработанных запросов DELETE /api/user/urls
	// Задам хардкодом, пока не придумаю подходящее имя флага или переменной
	svcConfig.DeadLetterLogFile = "emergency_dead_letter.log"

	appConfig := ApplicationConfig{ServiceConfig: svcConfig}

	parseFlags(&appConfig)

	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		appConfig.LogLevel = envLogLevel
	}

	if envServAddr := os.Getenv("SERVER_ADDRESS"); envServAddr != "" {
		appConfig.ServerRunAddress = envServAddr
	}

	if envBaseURL := os.Getenv("BASE_URL"); envBaseURL != "" {
		appConfig.TargetBaseURL = envBaseURL
	}

	if envDatabaseDSN := os.Getenv("DATABASE_DSN"); envDatabaseDSN != "" {
		appConfig.DatabaseDSN = envDatabaseDSN
	}

	if envFileStoragePath := os.Getenv("FILE_STORAGE_PATH"); envFileStoragePath != "" {
		appConfig.FileStoragePath = envFileStoragePath
	}

	if envCookieUserIDName := os.Getenv("COOKIE_USER_ID"); envCookieUserIDName != "" {
		appConfig.CookieUserIDName = envCookieUserIDName
	}

	if envSigningKey := []byte(os.Getenv("SIGNING_KEY")); len(envSigningKey) != 0 {
		appConfig.SigningKey = envSigningKey
	} else {
		// Т.к. автотесты на платформе должны стартовать со значениями по умолчанию,
		// пока также добавлю ветвь с установкой ключа в явном виде
		appConfig.SigningKey = []byte("jwt-signing-super-secret")
	}

	if envEncryptingKey := []byte(os.Getenv("ENCRYPTING_KEY")); len(envEncryptingKey) == 32 {
		appConfig.EncryptingKey = envEncryptingKey
	} else {
		// Т.к. автотесты на платформе должны стартовать со значениями по умолчанию,
		// пока также добавлю ветвь с установкой ключа в явном виде
		appConfig.EncryptingKey = []byte("amustbe32byteslongsecretkey26.!?")
	}

	if envTickPeriod := os.Getenv("SAVE_STATE_INTERVAL"); envTickPeriod != "" {
		ssp, err := time.ParseDuration(envTickPeriod)
		if err != nil {
			return ApplicationConfig{}, err
		}
		appConfig.SaveStateInterval = ssp
	}

	// Пока ограничусь значением по умолчанию, т.е. без параметризации из переменных среды / параметров запуска
	appConfig.ShutdownTimeout = 10 * time.Second

	// Аналогично TTL cookie с ID пользователя задам пока в коде
	appConfig.CookieUserIDTTL = 24 * time.Hour

	return appConfig, nil
}

func parseFlags(cfg *ApplicationConfig) {
	flag.StringVar(&cfg.ServerRunAddress, "a", ":8080", "address and port to run server")
	flag.StringVar(&cfg.TargetBaseURL, "b", "http://localhost:8080", "target URL base path")
	flag.StringVar(&cfg.LogLevel, "l", "info", "log level")
	flag.StringVar(&cfg.DatabaseDSN, "d", "", "data source name")
	flag.StringVar(&cfg.FileStoragePath, "f", "", "path to storage saved state")
	flag.StringVar(&cfg.CookieUserIDName, "u", "user_id_jwot", "user ID cookie name")
	flag.DurationVar(&cfg.SaveStateInterval, "s", time.Second*time.Duration(1), "save state interval")
	flag.Parse()
}

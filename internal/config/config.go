package config

import (
	"flag"
	"os"
	"time"

	"github.com/m-j-majevsky/url-shortener/internal/service"
)

type ApplicationConfig struct {
	LogLevel         string
	ServerRunAddress string
	TargetBaseURL    string
	FileStoragePath  string
	SaveStatePeriod  time.Duration
	ShutdownTimeout  time.Duration // время на graceful shutdown
	ServiceConfig    service.ShortenerConfig
}

func LoadApplicationConfig() (ApplicationConfig, error) {
	svcConfig := service.DefaultShortenerConfig()

	appConfig := ApplicationConfig{ServiceConfig: svcConfig}

	parseFlags(&appConfig)

	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		appConfig.LogLevel = envLogLevel
	}

	if envServAddr := os.Getenv("SERVER_ADDRESS"); envServAddr != "" {
		appConfig.ServerRunAddress = envServAddr
	}

	if envBaseUrl := os.Getenv("BASE_URL"); envBaseUrl != "" {
		appConfig.TargetBaseURL = envBaseUrl
	}

	if envFileStoragePath := os.Getenv("FILE_STORAGE_PATH"); envFileStoragePath != "" {
		appConfig.FileStoragePath = envFileStoragePath
	}

	if envTickPeriod := os.Getenv("SAVE_STATE_PERIOD"); envTickPeriod != "" {
		ssp, err := time.ParseDuration(envTickPeriod)
		if err != nil {
			return ApplicationConfig{}, err
		}
		appConfig.SaveStatePeriod = ssp
	}

	appConfig.ShutdownTimeout = 10 * time.Second

	return appConfig, nil
}

func parseFlags(cfg *ApplicationConfig) {
	flag.StringVar(&cfg.ServerRunAddress, "a", ":8080", "address and port to run server")
	flag.StringVar(&cfg.TargetBaseURL, "b", "http://localhost:8080", "target URL base path")
	flag.StringVar(&cfg.LogLevel, "l", "info", "log level")
	flag.StringVar(&cfg.FileStoragePath, "f", "storage_state.json", "path to storage saved state")
	flag.DurationVar(&cfg.SaveStatePeriod, "s", time.Second*time.Duration(1), "save state period")
	flag.Parse()
}

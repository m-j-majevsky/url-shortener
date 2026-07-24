package config

import (
	"flag"
	"os"

	"github.com/m-j-majevsky/url-shortener/internal/service"
)

type ApplicationConfig struct {
	LogLevel         string
	ServerRunAddress string
	TargetBaseURL    string
	ServiceConfig    service.ShortenerConfig
}

func MakeApplicationConfig() ApplicationConfig {
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

	return appConfig
}

func parseFlags(cfg *ApplicationConfig) {
	flag.StringVar(&cfg.ServerRunAddress, "a", ":8080", "address and port to run server")
	flag.StringVar(&cfg.TargetBaseURL, "b", "http://localhost:8080", "target URL base path")
	flag.StringVar(&cfg.LogLevel, "l", "info", "log level")
	flag.Parse()
}

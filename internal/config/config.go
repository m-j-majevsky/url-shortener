package config

import (
	"flag"

	"github.com/m-j-majevsky/url-shortener/internal/service"
)

type ApplicationConfig struct {
	ServerRunAddress string
	TargetBaseURL    string
	ServiceConfig    service.ShortenerConfig
}

func MakeApplicationConfig() ApplicationConfig {
	svcConfig := service.DefaultShortenerConfig()

	appConfig := ApplicationConfig{ServiceConfig: svcConfig}

	parseFlags(&appConfig)

	return appConfig
}

func parseFlags(cfg *ApplicationConfig) {
	flag.StringVar(&cfg.ServerRunAddress, "a", ":8080", "address and port to run server")
	flag.StringVar(&cfg.TargetBaseURL, "b", "http://localhost:8080", "target URL base path")
	flag.Parse()
}

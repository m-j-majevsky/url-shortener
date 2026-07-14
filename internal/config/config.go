package config

import (
	"flag"

	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

type ApplicationConfig struct {
	ServerRunAddress string
	TargetURLBase    string
	Storage          repository.URLStorage
}

func ConfigureApplication() ApplicationConfig {
	config := ApplicationConfig{}

	config.Storage = repository.NewURLStorage(0)

	parseFlags(&config)

	return config
}

func parseFlags(cfg *ApplicationConfig) {
	flag.StringVar(&cfg.ServerRunAddress, "a", ":8080", "address and port to run server")
	flag.StringVar(&cfg.TargetURLBase, "b", "http://localhost:8080", "address and port to run server")
	flag.Parse()
}

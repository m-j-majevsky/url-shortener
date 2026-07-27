package main

import (
	"log"
	"net/http"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
	"go.uber.org/zap"
)

func main() {
	cfg := configure()

	if err := logger.Initialize(cfg.LogLevel); err != nil {
		log.Fatal(err)
	}

	err := run(cfg)
	logger.Log.Fatal(err.Error(), zap.String("event", "start server"))
	logger.Log.Sync()
}

func configure() config.ApplicationConfig {
	cfg := config.MakeApplicationConfig()
	cfg.ServiceConfig.Storage = repository.NewStorage()
	return cfg
}

func run(cfg config.ApplicationConfig) error {
	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		return err
	}

	rt := handler.NewRouter(svc, cfg.TargetBaseURL)

	logger.Log.Info("Running server", zap.String("address", cfg.ServerRunAddress))

	return http.ListenAndServe(cfg.ServerRunAddress, logger.WithLogging(handler.GzipMiddleware(rt)))
}

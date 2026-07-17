package main

import (
	"log"
	"net/http"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

func main() {
	log.Fatal(run())
}

func run() error {
	cfg := config.MakeApplicationConfig()
	cfg.ServiceConfig.Storage = repository.NewStorage()

	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		return err
	}

	rt := handler.NewRouter(svc, cfg.TargetBaseURL)

	return http.ListenAndServe(cfg.ServerRunAddress, rt)
}

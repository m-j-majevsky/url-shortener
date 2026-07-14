package main

import (
	"log"
	"net/http"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
)

func main() {
	log.Fatal(run())
}

func run() error {
	cfg := config.ConfigureApplication()
	ctx := handler.NewRouterContext(cfg.Storage, cfg.TargetURLBase)
	return http.ListenAndServe(cfg.ServerRunAddress, ctx)
}

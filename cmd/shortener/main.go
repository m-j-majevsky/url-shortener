package main

import (
	"log"
	"net/http"

	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

func main() {
	log.Fatal(run())
}

func run() error {
	storage := repository.ProvideURLStorage()
	webhook := http.HandlerFunc(handler.CreateWebhook(storage))
	return http.ListenAndServe(`:8080`, webhook)
}

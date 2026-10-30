package handler_test

// Файл содержит Example-функции пакета handler: примеры обращения к HTTP API
// сервиса. Обзорный пример собран здесь; примеры отдельных эндпоинтов
// добавляются в соседних файлах example_*.go.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

// exampleConfig — конфигурация приложения для примеров; baseURL — адрес
// тестового сервера, по которому сервис собирает сокращённые ссылки.
func exampleConfig(baseURL string) config.ApplicationConfig {
	return config.ApplicationConfig{
		TargetBaseURL:    baseURL,
		CookieUserIDName: "user_id_jwt",
		SigningKey:       []byte("example-signing-key"),
		CookieUserIDTTL:  24 * time.Hour,
	}
}

// newExampleServer собирает полный стек сервиса: LocalStorage ->
// service.Shortener -> handler.Router, и поднимает его на тестовом
// HTTP-сервере httptest. Адрес сервера выдается слушателю до сборки
// роутера, поэтому TargetBaseURL в конфигурации совпадает с ts.URL —
// переход по сокращённой ссылке внутри примера реально разрешается.
// Остановить сервер вызывающий код должен через defer ts.Close().
func newExampleServer() *httptest.Server {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	baseURL := "http://" + l.Addr().String()
	cfg := exampleConfig(baseURL)

	svcCfg := service.DefaultShortenerConfig()
	svcCfg.Storage = repository.NewLocalStorage()

	svc, err := service.NewShortener(svcCfg)
	if err != nil {
		panic(err)
	}

	rt := handler.NewRouter(handler.NewRouterParams(cfg, svc, nil, zap.NewNop()))

	ts := httptest.NewUnstartedServer(rt)
	ts.Listener = l
	ts.Start()
	return ts
}

// must паникует при err != nil: ошибка в работающем примере — дефект
// самого примера, продолжать его выполнение бессмысленно.
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// Example — обзорный пример работы с HTTP API сервиса: проверка доступности,
// сокращение ссылки и переход по сокращённому адресу.
func Example() {
	ts := newExampleServer()
	defer ts.Close()

	cli := exampleClient()

	// Проверка доступности сервиса.
	resp, err := cli.Get(ts.URL + "/ping")
	must(err)
	fmt.Printf("GET /ping: %d\n", resp.StatusCode)
	resp.Body.Close()

	// Сокращение ссылки JSON-запросом. Пользователь анонимный:
	// CookieMiddleware сам выпустит cookie с его идентификатором и
	// вернёт её в ответе, дальше jar переиспользует её автоматически.
	resp, err = cli.Post(ts.URL+"/api/shorten", "application/json",
		strings.NewReader(`{"url": "https://yandex.ru"}`))
	must(err)
	var shortened model.ShortenRes
	must(json.NewDecoder(resp.Body).Decode(&shortened))
	resp.Body.Close()
	fmt.Printf("POST /api/shorten: %d\n", resp.StatusCode)

	// Переход по сокращённому адресу ведёт на оригинальную ссылку.
	// ShortenRes.Result собран сервисом из TargetBaseURL, равного ts.URL,
	// поэтому запрос попадает на тот же тестовый сервер.
	resp, err = cli.Get(shortened.Result)
	must(err)
	fmt.Printf("GET /<token>: %d, Location: %s\n",
		resp.StatusCode, resp.Header.Get("Location"))
	resp.Body.Close()

	// Output:
	// GET /ping: 200
	// POST /api/shorten: 201
	// GET /<token>: 307, Location: https://yandex.ru
}

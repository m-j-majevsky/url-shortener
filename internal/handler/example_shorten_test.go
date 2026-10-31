package handler_test

// Файл содержит примеры сокращения одной ссылки: текстовым API (POST "/")
// и JSON API (POST "/api/shorten"), включая повторную отправку той же
// ссылки — сервис возвращает существующий токен, хендлер отвечает 409.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

// exampleClient собирает HTTP-клиент с cookie-jar: CookieMiddleware при первом
// запросе выпускает cookie пользователя, jar переиспользует её дальше —
// все запросы примера идут от одного пользователя.
func exampleClient() *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}
	return &http.Client{
		Jar: jar,
		// Не следуем редиректам: пример показывает код ответа, а не конечную страницу.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Example_shortenText — текстовый API: URL передаётся телом POST-запроса
// на "/", ответ — сокращённая ссылка в чистом виде. Повторная отправка
// той же ссылки отвечает 409 Conflict с той же короткой ссылкой.
func Example_shortenText() {
	ts := newExampleServer()
	defer ts.Close()
	cli := exampleClient()

	// Первое сокращение.
	resp, err := cli.Post(ts.URL+"/", "text/plain",
		strings.NewReader("https://practicum.yandex.ru"))
	must(err)
	shortened, err := io.ReadAll(resp.Body)
	must(err)
	resp.Body.Close()
	fmt.Printf("первый запрос: %d, ответ — ссылка на тестовый сервер: %v\n",
		resp.StatusCode, strings.HasPrefix(string(shortened), ts.URL+"/"))

	// Повторная отправка той же ссылки.
	resp, err = cli.Post(ts.URL+"/", "text/plain",
		strings.NewReader("https://practicum.yandex.ru"))
	must(err)
	shortenedAgain, err := io.ReadAll(resp.Body)
	must(err)
	resp.Body.Close()
	fmt.Printf("повторный запрос: %d, та же короткая ссылка: %v\n",
		resp.StatusCode, string(shortenedAgain) == string(shortened))

	// Output:
	// первый запрос: 201, ответ — ссылка на тестовый сервер: true
	// повторный запрос: 409, та же короткая ссылка: true
}

// Example_shortenJSON — JSON API: POST "/api/shorten" с телом {"url": ...}
// и заголовком Content-Type: application/json; ответ — {"result": ...}.
// Повторная отправка той же ссылки отвечает 409 с той же короткой ссылкой.
func Example_shortenJSON() {
	ts := newExampleServer()
	defer ts.Close()
	cli := exampleClient()

	shorten := func() (string, int) {
		resp, err := cli.Post(ts.URL+"/api/shorten", "application/json",
			strings.NewReader(`{"url": "https://practicum.yandex.ru"}`))
		must(err)
		defer resp.Body.Close()
		var res model.ShortenRes
		must(json.NewDecoder(resp.Body).Decode(&res))
		return res.Result, resp.StatusCode
	}

	first, firstCode := shorten()
	fmt.Printf("первый запрос: %d, ответ — ссылка на тестовый сервер: %v\n",
		firstCode, strings.HasPrefix(first, ts.URL+"/"))

	second, secondCode := shorten()
	fmt.Printf("повторный запрос: %d, та же короткая ссылка: %v\n",
		secondCode, second == first)

	// Output:
	// первый запрос: 201, ответ — ссылка на тестовый сервер: true
	// повторный запрос: 409, та же короткая ссылка: true
}

package handler_test

// Файл содержит пример работы со ссылками пользователя: список сокращённых
// ссылок (GET "/api/user/urls") и пакетное удаление (DELETE "/api/user/urls").
// Удаление асинхронное: хендлер принимает токены и отвечает 202 «принято»,
// фактическую пометку делает фоновый процессор при выгрузке батча, поэтому
// сразу после удаления список ещё содержит ссылки.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

// Example_userURLs показывает список ссылок пользователя и пакетное удаление.
func Example_userURLs() {
	ts := newExampleServer()
	defer ts.Close()
	cli := exampleClient()

	// Пользователь сокращает две ссылки текстовым API.
	shortened := make([]string, 0, 2)
	for _, target := range []string{"https://practicum.yandex.ru", "https://go.dev"} {
		resp, err := cli.Post(ts.URL+"/", "text/plain", strings.NewReader(target))
		must(err)
		body, err := io.ReadAll(resp.Body)
		must(err)
		resp.Body.Close()
		fmt.Printf("сокращение %q: %d\n", target, resp.StatusCode)
		shortened = append(shortened, strings.TrimSpace(string(body)))
	}

	// Список ссылок пользователя: 200, две записи.
	resp, err := cli.Get(ts.URL + "/api/user/urls")
	must(err)
	body, err := io.ReadAll(resp.Body)
	must(err)
	resp.Body.Close()
	var list []model.UserURLsResItem
	must(json.Unmarshal(body, &list))
	fmt.Printf("список: %d, записей: %d\n", resp.StatusCode, len(list))

	// Пакетное удаление: тело — массив токенов, Content-Type: application/json.
	tokens := make([]string, 0, len(shortened))
	for _, short := range shortened {
		tokens = append(tokens, strings.TrimPrefix(short, ts.URL+"/"))
	}
	payload, err := json.Marshal(tokens)
	must(err)
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/user/urls", bytes.NewReader(payload))
	must(err)
	req.Header.Set("Content-Type", "application/json")
	resp, err = cli.Do(req)
	must(err)
	resp.Body.Close()
	fmt.Printf("удаление: %d\n", resp.StatusCode)

	// Список сразу после удаления: 202 означает «принято в очередь», пометку
	// делает фоновый процессор при выгрузке батча — ссылки ещё на месте.
	resp, err = cli.Get(ts.URL + "/api/user/urls")
	must(err)
	body, err = io.ReadAll(resp.Body)
	must(err)
	resp.Body.Close()
	must(json.Unmarshal(body, &list))
	fmt.Printf("список после удаления: %d, записей: %d\n", resp.StatusCode, len(list))

	// Новый клиент без cookie — для middleware это новый пользователь,
	// его список пуст: 204 No Content.
	fresh := exampleClient()
	resp, err = fresh.Get(ts.URL + "/api/user/urls")
	must(err)
	resp.Body.Close()
	fmt.Printf("список нового пользователя: %d\n", resp.StatusCode)

	// Output:
	// сокращение "https://practicum.yandex.ru": 201
	// сокращение "https://go.dev": 201
	// список: 200, записей: 2
	// удаление: 202
	// список после удаления: 200, записей: 2
	// список нового пользователя: 204
}

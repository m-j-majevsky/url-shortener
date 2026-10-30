package handler_test

// Файл содержит пример батч-сокращения ссылок: POST "/api/shorten/batch".
// Конфликт по любому элементу пакета (URL уже сохранён в хранилище)
// переключает код ответа всего батча с 201 на 409, при этом ответ содержит
// все элементы: конфликтный возвращает прежнюю короткую ссылку,
// остальные сохраняются.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

// Example_batchShorten — батч-сокращение: массив пар correlation_id/original_url,
// в ответе — те же correlation_id и полные короткие ссылки. Повторная отправка
// уже сохранённого URL (в одном пакете с новым) даёт 409: конфликтный элемент
// возвращает прежнюю короткую ссылку, новый элемент сохраняется.
func Example_batchShorten() {
	ts := newExampleServer()
	defer ts.Close()
	cli := exampleClient()

	batch := func(items []model.BatchShortenReqItem) ([]model.BatchShortenResItem, int) {
		body, err := json.Marshal(items)
		must(err)
		resp, err := cli.Post(ts.URL+"/api/shorten/batch", "application/json", bytes.NewReader(body))
		must(err)
		defer resp.Body.Close()
		var res []model.BatchShortenResItem
		must(json.NewDecoder(resp.Body).Decode(&res))
		return res, resp.StatusCode
	}

	// Первый пакет: обе ссылки новые.
	first, code := batch([]model.BatchShortenReqItem{
		{CorrelationID: "1", OriginalURL: "https://practicum.yandex.ru"},
		{CorrelationID: "2", OriginalURL: "https://go.dev"},
	})
	allFromServer := true
	for _, it := range first {
		allFromServer = allFromServer && strings.HasPrefix(it.ShortURL, ts.URL+"/")
	}
	fmt.Printf("первый пакет: %d, обе ссылки от тестового сервера: %v\n", code, allFromServer)

	// Повторный пакет: URL первого элемента уже сохранён, второй — новый.
	second, code := batch([]model.BatchShortenReqItem{
		{CorrelationID: "3", OriginalURL: "https://practicum.yandex.ru"},
		{CorrelationID: "4", OriginalURL: "https://golang.org"},
	})
	var shortOf1, shortOf3 string
	for _, it := range first {
		if it.CorrelationID == "1" {
			shortOf1 = it.ShortURL
		}
	}
	for _, it := range second {
		if it.CorrelationID == "3" {
			shortOf3 = it.ShortURL
		}
	}
	fmt.Printf("повторный пакет: %d, прежняя ссылка для существующего URL: %v\n",
		code, shortOf3 != "" && shortOf3 == shortOf1)

	// Output:
	// первый пакет: 201, обе ссылки от тестового сервера: true
	// повторный пакет: 409, прежняя ссылка для существующего URL: true
}

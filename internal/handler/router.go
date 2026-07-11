package handler

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

var storage model.URLStorage

func CreateWebhook(s model.URLStorage) func(http.ResponseWriter, *http.Request) {
	storage = s
	return webhook
}

// функция-обработчик HTTP-запроса
func webhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")

	switch url := r.URL.String(); r.Method {

	case http.MethodPost:
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
			return
		}
		if ok, err := isValidPostRequest(url, bodyBytes); !ok {
			http.Error(w, err, http.StatusBadRequest)
		}
		handlePost(w, r.Host, bodyBytes)

	case http.MethodGet:
		if ok, err := isValidGetRequest(url); !ok {
			http.Error(w, err, http.StatusBadRequest)
			return
		}
		handleGet(w, url)

	default:
		http.Error(w, "недопустимый метод", http.StatusBadRequest)

	}
}

func handleGet(w http.ResponseWriter, url string) {
	longURL, err := service.ResolveShortURL(storage, model.ShortURL(url[1:]))

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Location", string(longURL))
	w.WriteHeader(http.StatusTemporaryRedirect)
}

func isValidGetRequest(url string) (bool, string) {
	segments := strings.Split(path.Clean(url), "/")

	if len(segments) != 2 || len(segments[0]) != 0 || len(segments[1]) != 8 {
		return false, "неверный формат URL: ожидается /<токен-из-восьми-base62-символов>"
	}

	if base62err := base62.ValidateBase62(segments[1]); base62err != nil {
		return false, fmt.Sprintf("недопустимый токен: %s", base62err)
	}

	return true, ""
}

func handlePost(w http.ResponseWriter, host string, body []byte) {
	token, shortenerErr := service.ShortenURL(storage, model.LongURL(body))
	if shortenerErr != nil {
		http.Error(w, shortenerErr.Error(), http.StatusBadRequest)
		return
	}

	shortURL := fmt.Sprintf("http://%s/%s", host, token)
	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, shortURL)
}

func isValidPostRequest(url string, body []byte) (bool, string) {
	if url != "/" {
		return false, `неверный формат URL: ожидается "/"`
	}

	if len(body) == 0 {
		return false, "тело запроса не может быть пустым"
	}

	return true, ""
}

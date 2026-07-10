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

func init() {
	storage = service.ProvideURLStorage()
}

// функция-обработчик HTTP-запроса
func Webhook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")

	if ok, err := validateRequest(r); !ok {
		http.Error(w, err, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPost:
		handlePost(w, r)
	case http.MethodGet:
		handleGet(w, r)
	}
}

func validateRequest(r *http.Request) (bool, string) {
	switch r.Method {
	case http.MethodPost:
		return isValidPostRequest(r)
	case http.MethodGet:
		return isValidGetRequest(r)
	default:
		return false, "Method not allowed"
	}
}

func handleGet(w http.ResponseWriter, r *http.Request) {
	url := r.URL.String()[1:]
	longURL, err := storage.Get(model.ShortURL(url))

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	io.WriteString(w, string(longURL))
}

func isValidGetRequest(r *http.Request) (bool, string) {
	url := r.URL.String()

	segments := strings.Split(path.Clean(url), "/")

	if len(segments) != 2 || len(segments[0]) != 0 || len(segments[1]) != 8 {
		return false, "неверный формат URL: ожидается /<токен-из-восьми-base62-символов>"
	}

	if base62err := base62.ValidateBase62(segments[1]); base62err != nil {
		return false, fmt.Sprintf("недопустимый токен: %s", base62err)
	}

	return true, ""
}

func handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	token, shortenerErr := storage.Add(model.LongURL(body))
	if shortenerErr != nil {
		http.Error(w, shortenerErr.Error(), http.StatusBadRequest)
		return
	}

	shortURL := fmt.Sprintf("http://%s/%s", r.Host, token)
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, shortURL)
}

func isValidPostRequest(r *http.Request) (ok bool, err string) {
	ok = (r.URL.String() == "/")

	if !ok {
		err = "неверный формат URL: ожидается /"
	}

	return
}

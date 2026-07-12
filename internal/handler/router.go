package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

var (
	storage model.URLStorage
	baseURL string
)

func CreateRouter(s model.URLStorage, targetURLBase string) http.Handler {
	storage = s
	baseURL = targetURLBase

	r := chi.NewRouter()

	r.Group(func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/", shortenLongURL)
	})

	r.Get("/{token}", resolveShortURL)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый путь в URL", http.StatusBadRequest)
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый метод", http.StatusBadRequest)
	})

	return r
}

func contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		next.ServeHTTP(w, r)
	})
}

func resolveShortURL(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := base62.ValidateBase62(token); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	longURL, err := service.ResolveShortURL(storage, model.ShortURL(token))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, string(longURL), http.StatusTemporaryRedirect)
}

func shortenLongURL(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
		return
	}

	if len(bodyBytes) == 0 {
		http.Error(w, "тело запроса не может быть пустым", http.StatusBadRequest)
		return
	}

	token, shortenerErr := service.ShortenURL(storage, model.LongURL(bodyBytes))
	if shortenerErr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	shortURL := fmt.Sprintf("%s/%s", baseURL, token)

	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, shortURL)
}

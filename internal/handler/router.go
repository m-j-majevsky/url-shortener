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

var storage model.URLStorage

func CreateRouter(s model.URLStorage) http.Handler {
	storage = s

	r := chi.NewRouter()

	r.Group(func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/", handlePost)
	})

	r.Get("/{token}", handleGet)

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

func handleGet(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if ok, err := isValidGetRequest(token); !ok {
		http.Error(w, err, http.StatusBadRequest)
		return
	}

	longURL, err := service.ResolveShortURL(storage, model.ShortURL(token))

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, string(longURL), http.StatusTemporaryRedirect)
}

func isValidGetRequest(token string) (bool, string) {
	if base62err := base62.ValidateBase62(token); base62err != nil {
		return false, fmt.Sprintf("недопустимый токен: %s", base62err)
	}

	return true, ""
}

func handlePost(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
		return
	}

	if ok, err := isValidPostRequest(bodyBytes); !ok {
		http.Error(w, err, http.StatusBadRequest)
	}

	token, shortenerErr := service.ShortenURL(storage, model.LongURL(bodyBytes))

	if shortenerErr != nil {
		http.Error(w, shortenerErr.Error(), http.StatusBadRequest)
		return
	}

	shortURL := fmt.Sprintf("http://%s/%s", r.Host, token)

	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, shortURL)
}

func isValidPostRequest(body []byte) (bool, string) {
	if len(body) == 0 {
		return false, "тело запроса не может быть пустым"
	}
	return true, ""
}

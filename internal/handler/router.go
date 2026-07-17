package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	enc "github.com/m-j-majevsky/url-shortener/internal/encoding"
)

type URLShortener interface {
	GenerateAndStore(longURL string) (string, error)
	Resolve(token string) (string, bool)
}

type Router struct {
	mux     *chi.Mux
	service URLShortener
	baseURL string
}

func (rt Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}

func NewRouter(svc URLShortener, targetBaseURL string) Router {
	cr := chi.NewRouter()

	mux := Router{
		mux:     cr,
		service: svc,
		baseURL: targetBaseURL,
	}

	cr.Group(func(cr chi.Router) {
		cr.Use(contentTypeMiddleware)
		cr.Post("/", mux.shortenLongURL)
	})

	cr.Get("/{token}", mux.resolveShortURL)

	cr.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый путь в URL", http.StatusBadRequest)
	})

	cr.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый метод", http.StatusBadRequest)
	})

	return mux
}

func contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		next.ServeHTTP(w, r)
	})
}

func (rt *Router) resolveShortURL(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := enc.IsValidBase62(token); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	url, found := rt.service.Resolve(token)
	if !found {
		http.Error(w, "URL не зарегистрирован", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (rt *Router) shortenLongURL(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
		return
	}

	if len(bodyBytes) == 0 {
		http.Error(w, "тело запроса не может быть пустым", http.StatusBadRequest)
		return
	}

	token, shortenerErr := rt.service.GenerateAndStore(string(bodyBytes))
	if shortenerErr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	shortURL := fmt.Sprintf("%s/%s", rt.baseURL, token)

	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, shortURL)
}

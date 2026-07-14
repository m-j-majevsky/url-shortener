package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

type RouterContext struct {
	router  *chi.Mux
	storage repository.URLStorage
	baseURL string
}

func (rc RouterContext) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc.router.ServeHTTP(w, r)
}

func NewRouterContext(s repository.URLStorage, targetBaseURL string) RouterContext {
	r := chi.NewRouter()

	ctx := RouterContext{
		router:  r,
		storage: s,
		baseURL: targetBaseURL,
	}

	r.Group(func(r chi.Router) {
		r.Use(contentTypeMiddleware)
		r.Post("/", ctx.shortenLongURL)
	})

	r.Get("/{token}", ctx.resolveShortURL)

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый путь в URL", http.StatusBadRequest)
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый метод", http.StatusBadRequest)
	})

	return ctx
}

func contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		next.ServeHTTP(w, r)
	})
}

func (ctx *RouterContext) resolveShortURL(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := base62.ValidateBase62(token); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	longURL, found := service.ResolveShortURL(ctx.storage, token)
	if !found {
		http.Error(w, "URL не зарегистрирован", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, longURL, http.StatusTemporaryRedirect)
}

func (ctx *RouterContext) shortenLongURL(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
		return
	}

	if len(bodyBytes) == 0 {
		http.Error(w, "тело запроса не может быть пустым", http.StatusBadRequest)
		return
	}

	token, shortenerErr := service.ShortenURL(ctx.storage, string(bodyBytes))
	if shortenerErr != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	shortURL := fmt.Sprintf("%s/%s", ctx.baseURL, token)

	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, shortURL)
}

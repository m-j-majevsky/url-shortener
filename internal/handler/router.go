package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	valid "github.com/asaskevich/govalidator/v12"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	enc "github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

const (
	ContentType = "Content-Type"
	AppJson     = "application/json"
	TextPlain   = "text/plain"
)

type URLShortener interface {
	GenerateAndStore(longURL string) (string, error)
	Resolve(token string) (string, bool)
	PingContext(ctx context.Context) error
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
		cr.Use(func(next http.Handler) http.Handler {
			return responseContentTypeMiddleware(next, TextPlain)
		})
		cr.Post("/", mux.shortenLongURLForText)
	})

	cr.Group(func(cr chi.Router) {
		cr.Use(func(next http.Handler) http.Handler {
			return responseContentTypeMiddleware(next, AppJson)
		})
		cr.Post("/api/shorten", mux.shortenLongURLForJson)
	})

	cr.Get("/{token}", mux.resolveShortURL)

	cr.Get("/ping", mux.pingDatabase)

	cr.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый путь в URL", http.StatusBadRequest)
	})

	cr.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "недопустимый метод", http.StatusBadRequest)
	})

	return mux
}

func (rt *Router) pingDatabase(w http.ResponseWriter, r *http.Request) {
	// Дедлайн на проверку БД - чтобы не висело вечно
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	err := rt.service.PingContext(ctx)
	if err != nil {
		logger.Log.Error("database ping failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (rt *Router) resolveShortURL(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := enc.IsValidBase62(token); err != nil {
		logger.Log.Debug("error validating URL token", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	url, found := rt.service.Resolve(token)
	if !found {
		logger.Log.Debug("cannot find token " + url)
		http.Error(w, "URL не зарегистрирован", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (rt *Router) shortenLongURLForText(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)

	if err != nil {
		logger.Log.Debug("error reading request body", zap.Error(err))
		http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
		return
	}

	if len(bodyBytes) == 0 {
		logger.Log.Debug("cannot process request with empty body")
		http.Error(w, "тело запроса не может быть пустым", http.StatusBadRequest)
		return
	}

	token, shortenerErr := rt.service.GenerateAndStore(string(bodyBytes))
	if shortenerErr != nil {
		logger.Log.Debug("error providing short URL", zap.Error(shortenerErr))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, fmt.Sprintf("%s/%s", rt.baseURL, token))
}

func (rt *Router) shortenLongURLForJson(w http.ResponseWriter, r *http.Request) {
	if rct := r.Header.Get(ContentType); rct != AppJson {
		logger.Log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	var req model.PostApiShortenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Log.Debug("cannot decode request JSON body", zap.Error(err))
		http.Error(w, "ожидается валидный JSON объект в теле запроса", http.StatusBadRequest)
		return
	}
	if isValidReqBody, validatorErr := valid.ValidateStruct(req); validatorErr != nil || !isValidReqBody {
		logger.Log.Debug("request validation error", zap.Error(validatorErr))
		http.Error(w, `в теле запроса ожидается JSON объект c ключом "url" и корректным значением в нем`, http.StatusBadRequest)
		return
	}

	token, shortenerErr := rt.service.GenerateAndStore(req.URL)
	if shortenerErr != nil {
		logger.Log.Debug("error providing short URL", zap.Error(shortenerErr))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	resp := model.PostApiShortenRes{
		Result: fmt.Sprintf("%s/%s", rt.baseURL, token),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Log.Debug("error encoding response", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}

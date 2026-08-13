package handler

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

const (
	ContentType = "Content-Type"
	AppJson     = "application/json"
	TextPlain   = "text/plain"

	pingPath = "/ping"

	procTimeout = 5 * time.Second
)

type URLShortener interface {
	GenerateAndStore(ctx context.Context, longURL string) (string, error)
	Resolve(ctx context.Context, token string) (string, error)
	WithDB() bool
	PingDB(ctx context.Context) error
}

type Router struct {
	mux               *chi.Mux
	service           URLShortener
	withRemoteStorage bool
	baseURL           string
}

func (rt Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}

var (
	badRequest = func(message string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, message, http.StatusBadRequest)
		}
	}

	badRequestWithWrongPath = badRequest("недопустимый путь в URL")

	badRequestWithMethodNotAllowed = badRequest("недопустимый метод")
)

func NewRouter(svc URLShortener, targetBaseURL string) Router {
	cr := chi.NewRouter()

	mux := Router{
		mux:               cr,
		service:           svc,
		withRemoteStorage: svc.WithDB(),
		baseURL:           targetBaseURL,
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

	if mux.withRemoteStorage {
		logger.Log.Info("service supports request for " + pingPath)
		cr.Get(pingPath, mux.pingDB)
	} else {
		logger.Log.Info("service doesn't support request for " + pingPath)
		cr.Get(pingPath, badRequestWithWrongPath)
	}

	cr.NotFound(badRequestWithWrongPath)

	cr.MethodNotAllowed(badRequestWithMethodNotAllowed)

	return mux
}

func (rt *Router) pingDB(w http.ResponseWriter, r *http.Request) {
	if !rt.withRemoteStorage {
		logger.Log.Error("service doesn't support method PingDB")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	err := rt.service.PingDB(ctx)
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

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	url, err := rt.service.Resolve(ctx, token)

	if err == nil {
		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
		return
	}

	var etnf *repository.ErrTokenNotFound
	if errors.As(err, &etnf) {
		logger.Log.Debug(fmt.Sprintf("token %s not found", token), zap.Error(err))
		http.Error(w, "URL не зарегистрирован", http.StatusBadRequest)
		return
	}

	logger.Log.Debug("error resolving token "+token, zap.Error(err))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
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

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	token, shortenerErr := rt.service.GenerateAndStore(ctx, string(bodyBytes))
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

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	token, shortenerErr := rt.service.GenerateAndStore(ctx, req.URL)
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

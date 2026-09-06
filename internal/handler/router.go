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

	"github.com/m-j-majevsky/url-shortener/internal/config"
	enc "github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

const (
	ContentType = "Content-Type"
	AppJSON     = "application/json"
	TextPlain   = "text/plain"

	pingPath = "/ping"

	procTimeout = 5 * time.Second
)

type URLShortener interface {
	GenerateAndStore(ctx context.Context, longURL, userID string) (string, error)
	BatchStore(ctx context.Context, batch model.BatchShortenReq, userID string) (model.BatchShortenRes, error)
	Resolve(ctx context.Context, token string) (string, error)
	ListUserURLs(ctx context.Context, userID string) (model.UserURLsRes, error)
	MarkUserURLsDeleted(ctx context.Context, batch model.TokensToMarkDeleted, userID string) error
}

type ServiceConfigReader interface {
	GetConfig() service.ShortenerConfig
}

type StoragePinger interface {
	Ping(ctx context.Context) error
}

type UserStorage interface {
	CheckUserExists(ctx context.Context, uid string) (bool, error)
	CreateUser(ctx context.Context) (string, error)
}

type UndeletedItemsHandler interface {
	HandleErrTokenLeftUndeletedAndLogData(err error) error
}

type UserCookieParams struct {
	UserCookieName string
	UserCookieTTL  time.Duration
	SigningKey     []byte
	userIDKey      contextKey
}

func NewUserCookieParams(cfg config.ApplicationConfig) UserCookieParams {
	return UserCookieParams{
		UserCookieName: cfg.CookieUserIDName,
		UserCookieTTL:  cfg.CookieUserIDTTL,
		SigningKey:     cfg.SigningKey,
	}
}

type RouterParams struct {
	Service    URLShortener
	BaseURL    string
	UserCookie UserCookieParams
}

func NewRouterParams(cfg config.ApplicationConfig, svc URLShortener) RouterParams {
	return RouterParams{
		Service:    svc,
		BaseURL:    cfg.TargetBaseURL,
		UserCookie: NewUserCookieParams(cfg),
	}
}

type contextKey string

type Router struct {
	mux       *chi.Mux
	service   URLShortener
	pinger    StoragePinger
	baseURL   string
	userIDKey contextKey
	uiHandler UndeletedItemsHandler
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

func NewRouter(params RouterParams) (*Router, error) {
	cr := chi.NewRouter()

	cr.Use(logger.WithLogging)
	cr.Use(GzipMiddleware)

	us, err := getUserStorage(params.Service)
	if err != nil {
		return nil, err
	}

	uih, ok := params.Service.(UndeletedItemsHandler)
	if !ok {
		return nil, fmt.Errorf("сервис не поддерживает интерфейс UndeletedItemsHandler")
	}

	params.UserCookie.userIDKey = contextKey(params.UserCookie.UserCookieName)
	mux := &Router{
		mux:       cr,
		service:   params.Service,
		pinger:    getStoragePinger(params.Service),
		baseURL:   params.BaseURL,
		userIDKey: params.UserCookie.userIDKey,
		uiHandler: uih,
	}

	// Текстовый API
	cr.Route("/", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return responseContentTypeMiddleware(next, TextPlain)
		})
		r.Use(CookieMiddleware(us, params.UserCookie))

		r.Post("/", mux.shortenLongURLForText)
	})

	// JSON API
	cr.Route("/api", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return responseContentTypeMiddleware(next, AppJSON)
		})
		r.Use(CookieMiddleware(us, params.UserCookie))

		r.Route("/user/urls", func(rr chi.Router) {
			rr.Get("/", mux.getUserURLs)
			rr.Delete("/", mux.markUserURLsDeleted)
		})

		r.Post("/shorten", mux.shortenLongURLForJSON)
		r.Post("/shorten/batch", mux.shortenBatch)
	})

	cr.Get("/{token}", mux.resolveShortURL)

	if mux.pinger != nil {
		logger.Log.Info("service supports request for " + pingPath)
		cr.Get(pingPath, mux.pingDB)
	} else {
		logger.Log.Info("service doesn't support request for " + pingPath)
		cr.Get(pingPath, badRequestWithWrongPath)
	}

	cr.NotFound(badRequestWithWrongPath)
	cr.MethodNotAllowed(badRequestWithMethodNotAllowed)

	return mux, nil
}

func getServiceConfig(svc URLShortener) (service.ShortenerConfig, error) {
	scr, ok := svc.(ServiceConfigReader)
	if !ok {
		return service.ShortenerConfig{}, fmt.Errorf("невозможно получить ServiceConfigReader")
	}
	return scr.GetConfig(), nil
}

func getUserStorage(svc URLShortener) (UserStorage, error) {
	cfg, err := getServiceConfig(svc)
	if err != nil {
		return nil, err
	}

	us, ok := cfg.Storage.(UserStorage)
	if !ok {
		return nil, fmt.Errorf("хранилище не поддерживает интерфейс middleware.UserStorage")
	}

	return us, nil
}

func getStoragePinger(svc URLShortener) StoragePinger {
	cfg, err := getServiceConfig(svc)
	if err != nil {
		return nil
	}

	if _, ok := cfg.Storage.(StoragePinger); !ok {
		return nil
	}

	sp, ok := svc.(StoragePinger)
	if !ok {
		return nil
	}

	return sp
}

func (rt *Router) getUserIDFromContext(ctx context.Context) (val string, err error) {
	val, found := ctx.Value(rt.userIDKey).(string)
	if !found {
		err = fmt.Errorf("ключ %v не найден в вызываемом контексте", rt.userIDKey)
	}
	return
}

// Путь "/ping", GET

func (rt *Router) pingDB(w http.ResponseWriter, r *http.Request) {
	if rt.pinger == nil {
		logger.Log.Error("ping storage is not supported")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	err := rt.pinger.Ping(ctx)
	if err != nil {
		logger.Log.Error("database ping failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Путь "/{token}", GET

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
		logger.Log.Debug(fmt.Sprintf("token %s not found", token), zap.Error(etnf))
		http.Error(w, "URL не зарегистрирован", http.StatusBadRequest)
		return
	}

	var erid *repository.ErrTokenIsDeleted
	if errors.As(err, &erid) {
		logger.Log.Debug(fmt.Sprintf("token %s is deleted", token), zap.Error(erid))
		http.Error(w, fmt.Sprintf("Токен %s удален", erid.Token), http.StatusGone)
		return
	}

	logger.Log.Debug("error resolving token "+token, zap.Error(err))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// Путь "/", POST (текстовый api)

func (rt *Router) shortenLongURLForText(w http.ResponseWriter, r *http.Request) {
	userID, err := rt.getUserIDFromContext(r.Context())
	if err != nil {
		logger.Log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

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

	resCode := http.StatusCreated
	token, shortenerErr := rt.service.GenerateAndStore(ctx, string(bodyBytes), userID)
	if shortenerErr != nil {
		var eoue *repository.ErrOriginalURLExists
		if errors.As(shortenerErr, &eoue) {
			// В этом случае данные для пользователя есть,
			// но необходимо сменить статус ответа!
			resCode = http.StatusConflict
		} else {
			// Прочие конфликты и ошибки считаются внутренней проблемой сервиса
			logger.Log.Debug("error providing short URL", zap.Error(shortenerErr))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(resCode)
	resBody := fmt.Sprintf("%s/%s", rt.baseURL, token)
	io.WriteString(w, resBody)
}

// Путь "/api/shorten", POST

func (rt *Router) shortenLongURLForJSON(w http.ResponseWriter, r *http.Request) {
	userID, err := rt.getUserIDFromContext(r.Context())
	if err != nil {
		logger.Log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rct := r.Header.Get(ContentType); rct != AppJSON {
		logger.Log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	var req model.ShortenReq
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

	resCode := http.StatusCreated
	token, shortenerErr := rt.service.GenerateAndStore(ctx, req.URL, userID)
	if shortenerErr != nil {
		var eoue *repository.ErrOriginalURLExists
		if errors.As(shortenerErr, &eoue) {
			// В этом случае данные для пользователя есть,
			// но необходимо сменить статус ответа!
			resCode = http.StatusConflict
		} else {
			// Прочие конфликты и ошибки считаются внутренней проблемой сервиса
			logger.Log.Debug("error providing short URL", zap.Error(shortenerErr))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(resCode)
	resp := model.ShortenRes{
		Result: fmt.Sprintf("%s/%s", rt.baseURL, token),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Log.Debug("error encoding response", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}

// Путь "/api/shorten/batch", POST

func (rt *Router) shortenBatch(w http.ResponseWriter, r *http.Request) {
	userID, err := rt.getUserIDFromContext(r.Context())
	if err != nil {
		logger.Log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rct := r.Header.Get(ContentType); rct != AppJSON {
		logger.Log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	var batchReq model.BatchShortenReq
	if err := json.NewDecoder(r.Body).Decode(&batchReq); err != nil {
		logger.Log.Debug("cannot decode request JSON body", zap.Error(err))
		http.Error(w, "ожидается валидный JSON объект в теле запроса", http.StatusBadRequest)
		return
	}
	if err := validateBatchReq(batchReq); err != nil {
		logger.Log.Debug("request validation error", zap.Error(err))
		http.Error(w, `в теле запроса ожидается JSON c массивом объектов, имеющих ключи correlation_id (строка) и original_url (валидный URL)`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	batchRes, err := rt.service.BatchStore(ctx, batchReq, userID)
	if err != nil {
		logger.Log.Debug("error providing batch of short URLs", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	setBaseForShortenBatch(rt.baseURL, batchRes)
	w.WriteHeader(getShortenBatchStatusCode(batchRes))
	if err := json.NewEncoder(w).Encode(batchRes); err != nil {
		logger.Log.Debug("error encoding response", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
}

func getShortenBatchStatusCode(batchRes model.BatchShortenRes) int {
	for _, it := range batchRes {
		if it.ConflictedURL {
			return http.StatusConflict
		}
	}
	return http.StatusCreated
}

func validateBatchReq(batch model.BatchShortenReq) error {
	for _, item := range batch {
		if ok, err := valid.ValidateStruct(item); err != nil || !ok {
			return err
		}
	}
	return nil
}

func setBaseForShortenBatch(baseURL string, batch model.BatchShortenRes) {
	for i := range batch {
		batch[i].ShortURL = fmt.Sprintf("%s/%s", baseURL, batch[i].ShortURL)
	}
}

// Путь "/api/user/urls", GET

func (rt *Router) getUserURLs(w http.ResponseWriter, r *http.Request) {
	userID, err := rt.getUserIDFromContext(r.Context())
	if err != nil {
		logger.Log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	res, err := rt.service.ListUserURLs(ctx, userID)
	if err != nil {
		logger.Log.Debug("error providing shortened URLs for user "+userID, zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(getUserURLsStatusCode(&res))
	if len(res) != 0 {
		setBaseForUserURLs(rt.baseURL, res)
		err := json.NewEncoder(w).Encode(res)
		if err != nil {
			logger.Log.Debug("error encoding response", zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}
}

func getUserURLsStatusCode(res *model.UserURLsRes) int {
	// При отсутствии сокращённых пользователем URL
	// хендлер должен отдавать HTTP-статус 204 No Content.
	if len(*res) == 0 {
		return http.StatusNoContent
	}
	return http.StatusOK
}

func setBaseForUserURLs(baseURL string, res model.UserURLsRes) {
	for i := range res {
		res[i].ShortURL = fmt.Sprintf("%s/%s", baseURL, res[i].ShortURL)
	}
}

// Путь "/api/user/urls", DELETE

func (rt *Router) markUserURLsDeleted(w http.ResponseWriter, r *http.Request) {
	userID, err := rt.getUserIDFromContext(r.Context())
	if err != nil {
		logger.Log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rct := r.Header.Get(ContentType); rct != AppJSON {
		logger.Log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	var batchReq model.TokensToMarkDeleted
	if err := json.NewDecoder(r.Body).Decode(&batchReq); err != nil {
		logger.Log.Debug("cannot decode request JSON body", zap.Error(err))
		http.Error(w, "ожидается валидный JSON с массивом строк в теле запроса", http.StatusBadRequest)
		return
	}
	if err := validateTokensToBeDeleted(batchReq); err != nil {
		logger.Log.Debug("request validation error", zap.Error(err))
		http.Error(w, `в теле запроса ожидается JSON c массивом строк из Base62 символов`, http.StatusBadRequest)
		return
	}

	if err := rt.service.MarkUserURLsDeleted(r.Context(), batchReq, userID); err != nil {
		// В вызванном методе возможны ошибки, только если канал для удаления записей переполнен,
		// и тогда err будет содержать service.ErrTokenLeftUndeleted для каждой непопавшей в канал записи из batchReq.

		logger.Log.Debug("error while deleting data", zap.Error(err))

		// Добавляем в лог неудаленные записи во избежание потери запросов
		loggingErr := rt.uiHandler.HandleErrTokenLeftUndeletedAndLogData(err)
		if loggingErr != nil {
			logger.Log.Error("error while logging undeleted items", zap.Error(loggingErr), zap.String("event", "critical error"))
		}

		http.Error(w, "Сервер перегружен; запрос на удаление сохранен для последующей обработки", http.StatusAccepted)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func validateTokensToBeDeleted(batch model.TokensToMarkDeleted) error {
	for _, item := range batch {
		if err := enc.IsValidBase62(item); err != nil {
			logger.Log.Debug("error validating URL token", zap.Error(err))
			return err
		}
	}
	return nil
}

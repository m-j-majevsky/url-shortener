package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	valid "github.com/asaskevich/govalidator/v12"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/m-j-majevsky/url-shortener/internal/audit"
	"github.com/m-j-majevsky/url-shortener/internal/config"
	enc "github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

const (
	ContentType = "Content-Type"
	AppJSON     = "application/json"
	TextPlain   = "text/plain"

	pingPath = "/ping"

	procTimeout = 5 * time.Second

	// Ограничения на размер тел запросов (http.MaxBytesReader).
	//
	// GzipMiddleware декомпрессирует тело запроса до передачи управления хендлерам,
	// поэтому лимит применяется к распакованным данным: защита действует и на
	// gzip-бомбы — сервер не станет читать больше заданного, сколь бы большими
	// ни были сжатые исходные данные.
	maxURLBodyLen       = 2048    // текстовый URL в POST "/": длина URL ограничена 2048 символами
	maxJSONBodyLen      = 4096    // одиночный JSON в "/api/shorten": запас на JSON-обрамление вокруг URL
	maxBatchJSONBodyLen = 1 << 20 // массивы в "/api/shorten/batch" и "/api/user/urls": batch-запросы бывают длинными
)

// ---------------------------------------------------------------------------
// Интерфейсы сервиса.
// Подробный комментарий к методам см. в '../service/shortner.go'.
// ---------------------------------------------------------------------------

// ShortURLCreator — сокращение одного URL.
type ShortURLCreator interface {
	GenerateAndStore(ctx context.Context, longURL, userID string) (string, error)
}

// BatchShortURLCreator — пакетное сокращение URL.
type BatchShortURLCreator interface {
	BatchStore(ctx context.Context, batch model.BatchShortenReq, userID string) (model.BatchShortenRes, error)
}

// ShortURLGetter — разворот короткого токена в исходный URL.
type ShortURLGetter interface {
	Resolve(ctx context.Context, token string) (string, error)
}

// UserURLsLister — список сокращённых URL пользователя.
type UserURLsLister interface {
	ListUserURLs(ctx context.Context, userID string) (model.UserURLsRes, error)
}

// UserURLsDeleter — постановка токенов пользователя в очередь на удаление.
type UserURLsDeleter interface {
	MarkUserURLsDeleted(ctx context.Context, batch model.TokensToMarkDeleted, userID string) error
}

// UndeleteFinalizer — добивка истории неудалённых токенов
// (запросы DELETE /api/user/urls, не поместившиеся в очередь удаления).
type UndeleteFinalizer interface {
	HandleErrTokenLeftUndeletedAndLogData(err error) error
}

// Pinger — проверка доступности хранилища.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Shortener — составной интерфейс сервисного слоя, используемый хендлером.
type Shortener interface {
	ShortURLCreator
	BatchShortURLCreator
	ShortURLGetter
	UserURLsLister
	UserURLsDeleter
	UndeleteFinalizer
	UserRegistrator
	Pinger
}

// UserRegistrator — проверка существования и анонимная регистрация пользователя.
// Используется CookieMiddleware: пользователь создаётся автоматически,
// без явной аутентификации.
type UserRegistrator interface {
	CheckUserExists(ctx context.Context, uid string) (bool, error)
	CreateUser(ctx context.Context) (string, error)
}

type UserCookieParams struct {
	UserCookieName string
	UserCookieTTL  time.Duration
	SigningKey     []byte
}

func NewUserCookieParams(cfg config.ApplicationConfig) UserCookieParams {
	return UserCookieParams{
		UserCookieName: cfg.CookieUserIDName,
		UserCookieTTL:  cfg.CookieUserIDTTL,
		SigningKey:     cfg.SigningKey,
	}
}

type RouterParams struct {
	Service    Shortener
	Logger     *zap.Logger
	BaseURL    string
	UserCookie UserCookieParams
	Auditor    audit.Notifier // nil (значение по умолчанию) означает, что аудит отключён.
}

func NewRouterParams(cfg config.ApplicationConfig, svc Shortener, auditor audit.Notifier, log *zap.Logger) RouterParams {
	return RouterParams{
		Service:    svc,
		Logger:     log,
		BaseURL:    cfg.TargetBaseURL,
		UserCookie: NewUserCookieParams(cfg),
		Auditor:    auditor,
	}
}

type contextKey string

// userIDKey — ключ, под которым ID пользователя хранится в контексте запроса.
const userIDKey contextKey = "user_id"

type Router struct {
	mux     *chi.Mux
	service Shortener
	baseURL string
	auditor audit.Notifier // рассылка событий аудита; nil — аудит отключён
	log     *zap.Logger
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

func NewRouter(params RouterParams) *Router {
	log := params.Logger
	if log == nil {
		log = zap.NewNop()
	}

	cr := chi.NewRouter()

	cr.Use(logger.WithLogging(log))
	cr.Use(GzipMiddleware)

	mux := &Router{
		mux:     cr,
		service: params.Service,
		baseURL: params.BaseURL,
		auditor: params.Auditor,
		log:     log,
	}

	// API: CookieMiddleware применяется один раз на группу защищённых эндпоинтов
	cr.Group(func(r chi.Router) {
		r.Use(CookieMiddleware(mux.log, params.Service, params.UserCookie))

		// Текстовый API
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return responseContentTypeMiddleware(next, TextPlain)
			})
			r.Post("/", mux.shortenLongURLForText)
		})

		// JSON API
		r.Group(func(r chi.Router) {
			r.Use(func(next http.Handler) http.Handler {
				return responseContentTypeMiddleware(next, AppJSON)
			})

			r.Route("/user/urls", func(rr chi.Router) {
				rr.Get("/", mux.getUserURLs)
				rr.Delete("/", mux.markUserURLsDeleted)
			})

			r.Post("/shorten", mux.shortenLongURLForJSON)
			r.Post("/shorten/batch", mux.shortenBatch)
		})
	})

	cr.Get("/{token}", mux.resolveShortURL)

	mux.log.Info("service supports request for " + pingPath)
	cr.Get(pingPath, mux.pingDB)

	cr.NotFound(badRequestWithWrongPath)
	cr.MethodNotAllowed(badRequestWithMethodNotAllowed)

	return mux
}

// getUserIDFromContext извлекает ID пользователя из контекста запроса.
// Возвращает ID и nil при успехе, или пустую строку и ошибку, если ключ не найден.
func getUserIDFromContext(ctx context.Context) (string, error) {
	val, ok := ctx.Value(userIDKey).(string)
	if !ok {
		return "", fmt.Errorf("ID пользователя не найден в контексте")
	}
	return val, nil
}

// notifyAudit формирует событие аудита и отправляет его во все доступные
// приёмники (паттерн «Наблюдатель»). Вызывается хэндлерами после успешной
// обработки запроса. Если аудит не настроен (auditor == nil), метод ничего не делает.
func (rt *Router) notifyAudit(action, userID, url string) {
	if rt.auditor == nil {
		return
	}
	rt.auditor.Notify(audit.AuditEvent{
		TS:     time.Now().Unix(),
		Action: action,
		UserID: userID,
		URL:    url,
	})
}

// Путь "/ping", GET

func (rt *Router) pingDB(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	err := rt.service.Ping(ctx)
	if err != nil {
		rt.log.Error("database ping failed", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// Путь "/{token}", GET

func (rt *Router) resolveShortURL(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := enc.IsValidBase62(token); err != nil {
		rt.log.Debug("error validating URL token", zap.Error(err))
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	url, err := rt.service.Resolve(ctx, token)

	if err == nil {
		// Аудит: пользователь успешно прошёл по короткой ссылке — отправляем follow-событие.
		// user_id не заполняется: GET /{token} не проходит через CookieMiddleware,
		// и надёжно определить пользователя в этом запросе нельзя
		// (событие предусматривает пустой идентификатор — «если есть»).
		rt.notifyAudit(audit.ActionFollow, "", url)

		http.Redirect(w, r, url, http.StatusTemporaryRedirect)
		return
	}

	var etnf *service.ErrTokenNotFound
	if errors.As(err, &etnf) {
		rt.log.Debug(fmt.Sprintf("token %s not found", token), zap.Error(etnf))
		http.Error(w, "URL не зарегистрирован", http.StatusBadRequest)
		return
	}

	var erid *service.ErrTokenIsDeleted
	if errors.As(err, &erid) {
		rt.log.Debug(fmt.Sprintf("token %s is deleted", token), zap.Error(erid))
		http.Error(w, fmt.Sprintf("Токен %s удален", erid.Token), http.StatusGone)
		return
	}

	rt.log.Debug("error resolving token "+token, zap.Error(err))
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// Путь "/", POST (текстовый api)

func (rt *Router) shortenLongURLForText(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserIDFromContext(r.Context())
	if err != nil {
		rt.log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Ограничиваем размер тела запроса до чтения.
	r.Body = http.MaxBytesReader(w, r.Body, maxURLBodyLen)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		rt.log.Debug("error reading request body", zap.Error(err))
		http.Error(w, "ошибка чтения тела запроса", http.StatusBadRequest)
		return
	}

	body := strings.TrimSpace(string(bodyBytes))
	if len(body) == 0 {
		rt.log.Debug("cannot process request with empty body")
		http.Error(w, "тело запроса не может быть пустым", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	resCode := http.StatusCreated
	token, shortenerErr := rt.service.GenerateAndStore(ctx, body, userID)
	if shortenerErr != nil {
		var eoue *service.ErrOriginalURLExists
		if errors.As(shortenerErr, &eoue) {
			// В этом случае данные для пользователя есть,
			// но необходимо сменить статус ответа!
			resCode = http.StatusConflict
		} else {
			// Прочие конфликты и ошибки считаются внутренней проблемой сервиса
			rt.log.Debug("error providing short URL", zap.Error(shortenerErr))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(resCode)
	resBody := fmt.Sprintf("%s/%s", rt.baseURL, token)
	io.WriteString(w, resBody)

	// Аудит: запрос обработан успешно — отправляем shorten-событие с оригинальным URL.
	// Успешной считается и обработка с ответом 409 Conflict: ссылка зарегистрирована,
	// клиент получил короткий URL.
	rt.notifyAudit(audit.ActionShorten, userID, body)
}

// Путь "/api/shorten", POST

func (rt *Router) shortenLongURLForJSON(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserIDFromContext(r.Context())
	if err != nil {
		rt.log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rct := r.Header.Get(ContentType); rct != AppJSON {
		rt.log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	// Ограничиваем размер тела запроса до декодирования.
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyLen)

	var req model.ShortenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		rt.log.Debug("cannot decode request JSON body", zap.Error(err))
		http.Error(w, "ожидается валидный JSON объект в теле запроса", http.StatusBadRequest)
		return
	}
	if isValidReqBody, validatorErr := valid.ValidateStruct(req); validatorErr != nil || !isValidReqBody {
		rt.log.Debug("request validation error", zap.Error(validatorErr))
		http.Error(w, `в теле запроса ожидается JSON объект c ключом "url" и корректным значением в нем`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	resCode := http.StatusCreated
	token, shortenerErr := rt.service.GenerateAndStore(ctx, req.URL, userID)
	if shortenerErr != nil {
		var eoue *service.ErrOriginalURLExists
		if errors.As(shortenerErr, &eoue) {
			// В этом случае данные для пользователя есть,
			// но необходимо сменить статус ответа!
			resCode = http.StatusConflict
		} else {
			// Прочие конфликты и ошибки считаются внутренней проблемой сервиса
			rt.log.Debug("error providing short URL", zap.Error(shortenerErr))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	}

	resp := model.ShortenRes{
		Result: fmt.Sprintf("%s/%s", rt.baseURL, token),
	}
	// Сначала сериализуем ответ в буфер: если кодирование не удалось,
	// ещё можно честно ответить 500, не отправив заголовок с кодом 201.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		rt.log.Debug("error encoding response", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(resCode)
	if _, err := buf.WriteTo(w); err != nil {
		rt.log.Debug("error writing response body", zap.Error(err))
	}

	// Аудит: запрос обработан успешно — отправляем shorten-событие с оригинальным URL.
	// Успешной считается и обработка с ответом 409 Conflict: ссылка зарегистрирована,
	// клиент получил короткий URL.
	rt.notifyAudit(audit.ActionShorten, userID, req.URL)
}

// Путь "/api/shorten/batch", POST

func (rt *Router) shortenBatch(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserIDFromContext(r.Context())
	if err != nil {
		rt.log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rct := r.Header.Get(ContentType); rct != AppJSON {
		rt.log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	// Ограничиваем размер тела запроса до декодирования.
	r.Body = http.MaxBytesReader(w, r.Body, maxBatchJSONBodyLen)

	var batchReq model.BatchShortenReq
	if err := json.NewDecoder(r.Body).Decode(&batchReq); err != nil {
		rt.log.Debug("cannot decode request JSON body", zap.Error(err))
		http.Error(w, "ожидается валидный JSON объект в теле запроса", http.StatusBadRequest)
		return
	}
	if err := validateBatchReq(batchReq); err != nil {
		rt.log.Debug("request validation error", zap.Error(err))
		http.Error(w, `в теле запроса ожидается JSON c массивом объектов, имеющих ключи correlation_id (строка) и original_url (валидный URL)`, http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	batchRes, err := rt.service.BatchStore(ctx, batchReq, userID)
	if err != nil {
		rt.log.Debug("error providing batch of short URLs", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	setBaseForShortenBatch(rt.baseURL, batchRes)
	// Сначала сериализуем ответ в буфер: если кодирование не удалось,
	// ещё можно честно ответить 500, не отправив заголовок с кодом 201/409.
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(batchRes); err != nil {
		rt.log.Debug("error encoding response", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(getShortenBatchStatusCode(batchRes))
	if _, err := buf.WriteTo(w); err != nil {
		rt.log.Debug("error writing response body", zap.Error(err))
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
	userID, err := getUserIDFromContext(r.Context())
	if err != nil {
		rt.log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), procTimeout)
	defer cancel()

	res, err := rt.service.ListUserURLs(ctx, userID)
	if err != nil {
		rt.log.Debug("error providing shortened URLs for user "+userID, zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if len(res) != 0 {
		setBaseForUserURLs(rt.baseURL, res)
		// Сначала сериализуем ответ в буфер: если кодирование не удалось,
		// ещё можно честно ответить 500, не отправив заголовок с кодом 200.
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(res); err != nil {
			rt.log.Debug("error encoding response", zap.Error(err))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(getUserURLsStatusCode(&res))
		if _, err := buf.WriteTo(w); err != nil {
			rt.log.Debug("error writing response body", zap.Error(err))
		}
	} else {
		// Пустой список: только заголовок 204 No Content, без тела.
		w.WriteHeader(getUserURLsStatusCode(&res))
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
	userID, err := getUserIDFromContext(r.Context())
	if err != nil {
		rt.log.Debug("error reading request context", zap.Error(err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if rct := r.Header.Get(ContentType); rct != AppJSON {
		rt.log.Debug("wrong request Content-Type: " + rct)
		http.Error(w, "ожидается запрос с заголовком Content-Type: application/json", http.StatusBadRequest)
		return
	}

	// Ограничиваем размер тела запроса до декодирования.
	r.Body = http.MaxBytesReader(w, r.Body, maxBatchJSONBodyLen)

	var batchReq model.TokensToMarkDeleted
	if err := json.NewDecoder(r.Body).Decode(&batchReq); err != nil {
		rt.log.Debug("cannot decode request JSON body", zap.Error(err))
		http.Error(w, "ожидается валидный JSON с массивом строк в теле запроса", http.StatusBadRequest)
		return
	}
	if err := rt.validateTokensToBeDeleted(batchReq); err != nil {
		rt.log.Debug("request validation error", zap.Error(err))
		http.Error(w, `в теле запроса ожидается JSON c массивом строк из Base62 символов`, http.StatusBadRequest)
		return
	}

	if err := rt.service.MarkUserURLsDeleted(r.Context(), batchReq, userID); err != nil {
		// В вызванном методе возможны ошибки, только если канал для удаления записей переполнен,
		// и тогда err будет содержать service.ErrTokenLeftUndeleted для каждой непопавшей в канал записи из batchReq.

		rt.log.Debug("error while deleting data", zap.Error(err))

		// Добавляем в лог неудаленные записи во избежание потери запросов
		loggingErr := rt.service.HandleErrTokenLeftUndeletedAndLogData(err)
		if loggingErr != nil {
			rt.log.Error("error while logging undeleted items", zap.Error(loggingErr), zap.String("event", "critical error"))
		}

		http.Error(w, "Сервер перегружен; запрос на удаление сохранен для последующей обработки", http.StatusAccepted)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (rt *Router) validateTokensToBeDeleted(batch model.TokensToMarkDeleted) error {
	for _, item := range batch {
		if err := enc.IsValidBase62(item); err != nil {
			rt.log.Debug("error validating URL token", zap.Error(err))
			return err
		}
	}
	return nil
}

// package handler_test — тесты HTTP-роутера url-shortener.
//
// Тесты разбиты на два уровня:
//   - «реальные» — на настоящем сервисе (service.Shortener) с in-memory
//     хранилищем (repository.LocalStorage): сквозная проверка слоёв;
//   - «мок-тесты» — на сгенерированном mockery-моке композитного интерфейса
//     handler.Shortener (handler/mocks): точное управление ошибками и
//     числом вызовов. Незапланированный вызов сервиса уронит тест паникой.
//
// Покрытие: маршруты и коды ответов всех эндпоинтов, ошибки CookieMiddleware,
// аудит (сокращение/переход, включён и выключен), gzip (сжатие ответов, распаковка
// запросов, отказ от сжатия ошибок), лимиты размера тел, валидация токенов.
package handler_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap"

	"github.com/m-j-majevsky/url-shortener/internal/audit"
	"github.com/m-j-majevsky/url-shortener/internal/auth"
	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/handler/mocks"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

// Компиляторная проверка: mockery-мок реализует композитный интерфейс роутера.
var _ handler.Shortener = (*mocks.MockShortener)(nil)

type RouterTestSuite struct {
	suite.Suite
	storage service.Storage
}

func TestRouterTestSuite(t *testing.T) {
	suite.Run(t, new(RouterTestSuite))
}

var (
	userID        = "8222be97-0266-40d1-b069-54f29508de43"
	yandexToken   = "0123AbcD"
	yandexLongURL = "https://yandex.ru"
	deletedToken  = "QwertY23"
	mailLongURL   = "https://mail.ru"
	secretJWTKey  = []byte("your-jwt-signing-secret")
)

func (s *RouterTestSuite) SetupTest() {
	s.storage = repository.NewLocalStorage()
	if s.storage == nil {
		s.T().Fatal("Ошибка создания локального хранилища")
	}

	ctx := context.Background()

	// yandexToken зарегистрирован, mailLongURL свободен
	if err := s.storage.Store(ctx, yandexToken, yandexLongURL, userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}
	url, err := s.storage.Resolve(ctx, yandexToken)
	if err != nil || url != yandexLongURL {
		s.T().Fatal("Ошибка подготовки тестовых данных")
	}

	// deletedToken зарегистрирован и помечен удалённым
	if err := s.storage.Store(ctx, deletedToken, "http://to-be-deleted.com", userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}
	tdb := repository.ToMarkDeletedReqBatch{
		repository.ToMarkDeletedReqItem{Token: deletedToken, UserID: userID},
	}
	if err := s.storage.MarkUserURLsDeleted(ctx, tdb); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}
	_, err = s.storage.Resolve(ctx, deletedToken)
	s.Require().Error(err)
}

const targetBaseURL = "http://localhost:8080/test"

// MakeTestApplicationConfig собирает конфигурацию приложения для тестов роутера.
func MakeTestApplicationConfig() config.ApplicationConfig {
	return config.ApplicationConfig{
		TargetBaseURL:    targetBaseURL,
		CookieUserIDName: "user_id_jwot",
		SigningKey:       secretJWTKey,
		CookieUserIDTTL:  24 * time.Hour,
	}
}

// -----------------------------------------------------------------------------------------------
// Хелперы
// -----------------------------------------------------------------------------------------------

// newRealRouterServer поднимает роутер на настоящем сервисе и in-memory хранилище.
// Пакетные удаления настраиваются на немедленный флаш (DeletionBatchSize = 1).
func (s *RouterTestSuite) newRealRouterServer(auditor audit.Notifier) (*httptest.Server, *service.Shortener) {
	cfg := MakeTestApplicationConfig()

	svcCfg := service.DefaultShortenerConfig()
	svcCfg.Storage = s.storage
	svcCfg.DeletionBatchSize = 1
	svc, err := service.NewShortener(svcCfg)
	if err != nil {
		s.T().Fatalf("Ошибка создания тестового сервиса: %v", err)
	}

	rt := handler.NewRouter(handler.NewRouterParams(cfg, svc, auditor, zap.NewNop()))
	ts := httptest.NewServer(rt)
	s.T().Cleanup(ts.Close)

	// Процессор удалений NewShortener не запускает — это делает main.
	// В тестах запускаем явно: при DeletionBatchSize = 1 флаш происходит
	// сразу после попадания элемента в очередь. По завершении теста
	// отменяем контекст и закрываем очередь: процессор доводит накопленный
	// батч до хранилища и завершается (оба останова безопасны).
	procCtx, cancelProc := context.WithCancel(context.Background())
	go svc.StartItemsDeletionProcessor(procCtx)
	s.T().Cleanup(func() {
		cancelProc()
		svc.StopItemsDeletionProcessor()
	})
	return ts, svc
}

// newMockRouterServer поднимает роутер на mockery-моке сервиса.
func (s *RouterTestSuite) newMockRouterServer(m *mocks.MockShortener, auditor audit.Notifier) *httptest.Server {
	cfg := MakeTestApplicationConfig()
	rt := handler.NewRouter(handler.NewRouterParams(cfg, m, auditor, zap.NewNop()))
	ts := httptest.NewServer(rt)
	s.T().Cleanup(ts.Close)
	return ts
}

// newMockStorage создаёт mockery-мок композитного интерфейса Shortener,
// привязанный к текущему тесту: незапланированный вызов любого метода
// сервиса уронит тест паникой (нет заготовленного ответа).
func (s *RouterTestSuite) newMockStorage() *mocks.MockShortener {
	return mocks.NewMockShortener(s.T())
}

// isErrAutoRedirectDisabled распознаёт штатную ошибку resty
// при отключённом следовании редиректам.
func isErrAutoRedirectDisabled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "auto redirect is disabled")
}

// mustJWT выдаёт валидный JWT для cookie пользователя.
func (s *RouterTestSuite) mustJWT(uid string) string {
	tok, err := auth.GenerateUserIDJWT(uid, 24*time.Hour, secretJWTKey)
	s.Require().NoError(err)
	return tok
}

// postAPIShorten — POST /api/shorten с JSON-телом и верным Content-Type.
func (s *RouterTestSuite) postAPIShorten(ts *httptest.Server, body string) (*resty.Response, error) {
	return resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R().
		SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(body).
		Post(ts.URL + "/api/shorten")
}

// fakeNotifier — синхронный приёмник событий аудита для тестов.
type fakeNotifier struct {
	mu     sync.Mutex
	events []audit.AuditEvent
}

func (f *fakeNotifier) Notify(e audit.AuditEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeNotifier) snapshot() []audit.AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.AuditEvent(nil), f.events...)
}

// -----------------------------------------------------------------------------------------------
// Матрица маршрутов на реальном сервисе
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestWebhook() {
	ts, _ := s.newRealRouterServer(nil)

	testCases := []struct {
		name             string
		method           string
		reqURL           string
		reqBody          string
		erResBody        string
		erResHeader      string
		erResHeaderValue string
		erResCode        int
	}{
		// GET
		{
			name:      "невалидный URL (корень)",
			method:    http.MethodGet,
			reqURL:    "/",
			erResCode: http.StatusBadRequest,
		},
		{
			name:      "невалидный URL (содержит не ASCII символы)",
			method:    http.MethodGet,
			reqURL:    "/WasD\u2126123",
			erResCode: http.StatusBadRequest,
		},
		{
			name:      "невалидный URL (содержит символы вне base62)",
			method:    http.MethodGet,
			reqURL:    "/WasD01_3",
			erResCode: http.StatusBadRequest,
		},
		{
			name:      "невалидный путь в URL",
			method:    http.MethodGet,
			reqURL:    "/token/WasD01_3",
			erResCode: http.StatusBadRequest,
		},
		{
			name:      "валидный URL, но данные не найдены",
			method:    http.MethodGet,
			reqURL:    "/WasD0123",
			erResCode: http.StatusBadRequest,
		},
		{
			name:             "валидный URL, и данные найдены",
			method:           http.MethodGet,
			reqURL:           "/" + yandexToken,
			erResHeader:      "Location",
			erResHeaderValue: yandexLongURL,
			erResCode:        http.StatusTemporaryRedirect,
		},
		{
			name:      "токен ранее помечен удаленным",
			method:    http.MethodGet,
			reqURL:    "/" + deletedToken,
			erResCode: http.StatusGone,
		},
		// POST
		{
			name:      "невалидный URL (любой, отличный от корня)",
			method:    http.MethodPost,
			reqURL:    "/badURL",
			erResCode: http.StatusBadRequest,
		},
		{
			name:      "пустое тело запроса недопустимо",
			method:    http.MethodPost,
			reqURL:    "/",
			reqBody:   "",
			erResCode: http.StatusBadRequest,
		},
		{
			name:             "валидные данные (запрос text/plain)",
			method:           http.MethodPost,
			reqURL:           "/",
			reqBody:          "http://some.beautiful.url.io",
			erResBody:        targetBaseURL + "/",
			erResHeader:      handler.ContentType,
			erResHeaderValue: handler.TextPlain,
			erResCode:        http.StatusCreated,
		},
		{
			name:             "валидные данные, но конфликт в исходном URL (запрос text/plain)",
			method:           http.MethodPost,
			reqURL:           "/",
			reqBody:          yandexLongURL,
			erResBody:        targetBaseURL + "/" + yandexToken,
			erResHeader:      handler.ContentType,
			erResHeaderValue: handler.TextPlain,
			erResCode:        http.StatusConflict,
		},
		// DELETE
		{
			name:      "невалидный метод",
			method:    http.MethodDelete,
			reqURL:    "/WasD0123",
			erResCode: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		s.T().Run(fmt.Sprintf("%q: %s", tc.method, tc.name), func(t *testing.T) {
			// Учитывая формат ответа на запрос GET, отключаем следование редиректам
			cli := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy())

			req := cli.R()
			req.Method = tc.method
			req.URL = ts.URL + tc.reqURL
			req.Body = io.NopCloser(strings.NewReader(tc.reqBody))
			req.Header.Set("Accept-Encoding", "")

			resp, err := req.Send()

			require.Conditionf(t,
				func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
				"ошибка при создании HTTP-запроса к серверу: %s", err,
			)

			assert.Equal(t, tc.erResCode, resp.StatusCode(), "Код ответа не совпадает с ожидаемым")

			if tc.erResHeader != "" {
				assert.Equal(t, tc.erResHeaderValue,
					resp.Header().Get(tc.erResHeader), "Заголовок ответа не совпадает с ожидаемым")
			}
			if tc.erResBody != "" {
				if tc.erResCode == http.StatusCreated {
					// Успех: к базовому префиксу добавляется свежесгенерированный токен
					assert.True(t, strings.HasPrefix(resp.String(), tc.erResBody),
						"Тело ответа должно начинаться с %s; получено %q", tc.erResBody, resp.String())
				} else {
					assert.Equal(t, tc.erResBody, resp.String(), "Тело ответа не совпадает с ожидаемым")
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------------------------
// Реальный сервис: JSON-API, батч, список, удаление, ping
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestWebhook_shortenLongURLForJSON() {
	ts, _ := s.newRealRouterServer(nil)

	body := fmt.Sprintf(`{"url": %q}`, yandexLongURL)
	resp, err := s.postAPIShorten(ts, body)
	s.Require().NoError(err)
	s.Equal(http.StatusConflict, resp.StatusCode(), "повторная регистрация того же URL — конфликт")
	s.Contains(resp.String(), `"result":"`+targetBaseURL+"/"+yandexToken+`"`)

	body = fmt.Sprintf(`{"url": %q}`, mailLongURL)
	resp, err = s.postAPIShorten(ts, body)
	s.Require().NoError(err)
	s.Equal(http.StatusCreated, resp.StatusCode())
	s.Contains(resp.String(), `"result":"`+targetBaseURL+"/")

	testCases := []struct {
		name   string
		body   string
		ct     string
		erCode int
	}{
		{name: "неверный Content Type", body: `{"url": "https://example.com"}`, ct: "text/xml", erCode: http.StatusBadRequest},
		{name: "невалидный json в теле запроса", body: `{"url"`, ct: handler.AppJSON, erCode: http.StatusBadRequest},
		{name: "нет поля url в теле запроса", body: `{}`, ct: handler.AppJSON, erCode: http.StatusBadRequest},
		{name: "в теле запроса в поле url записан не URL", body: `{"url": "not-a-url"}`, ct: handler.AppJSON, erCode: http.StatusBadRequest},
	}
	for _, tc := range testCases {
		s.T().Run(tc.name, func(t *testing.T) {
			resp, err := resty.New().R().
				SetHeader(handler.ContentType, tc.ct).
				SetBody(tc.body).
				Post(ts.URL + "/api/shorten")
			require.NoError(t, err)
			assert.Equal(t, tc.erCode, resp.StatusCode())
		})
	}
}

func (s *RouterTestSuite) TestWebhook_shortenBatch() {
	ts, _ := s.newRealRouterServer(nil)

	batchIn := model.BatchShortenReq{
		{CorrelationID: "1", OriginalURL: "http://batch.one.com"},
		{CorrelationID: "2", OriginalURL: "http://batch.two.com"},
	}
	bin, err := json.Marshal(batchIn)
	s.Require().NoError(err)

	resp, err := resty.New().R().
		SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(bin).
		Post(ts.URL + "/api/shorten/batch")
	s.Require().NoError(err)
	s.Equal(http.StatusCreated, resp.StatusCode())

	var got model.BatchShortenRes
	s.Require().NoError(json.Unmarshal(resp.Body(), &got))
	s.Len(got, 2)
	for i, item := range got {
		s.Equal(batchIn[i].CorrelationID, item.CorrelationID)
		s.True(strings.HasPrefix(item.ShortURL, targetBaseURL+"/"))
		s.NotEqual(targetBaseURL+"/", item.ShortURL) // токен сгенерирован
	}

	// Конфликт по URL: элемент уже зарегистрирован под известным токеном
	batchIn = model.BatchShortenReq{
		{CorrelationID: "3", OriginalURL: yandexLongURL},
	}
	bin, err = json.Marshal(batchIn)
	s.Require().NoError(err)

	resp, err = resty.New().R().
		SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(bin).
		Post(ts.URL + "/api/shorten/batch")
	s.Require().NoError(err)
	s.Equal(http.StatusConflict, resp.StatusCode())

	got = nil
	s.Require().NoError(json.Unmarshal(resp.Body(), &got))
	s.Require().Len(got, 1)
	s.Equal(targetBaseURL+"/"+yandexToken, got[0].ShortURL, "при конфликте возвращается ранее сохранённый токен")

	// Невалидный батч: пустой correlation_id
	resp, err = resty.New().R().
		SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(`[{"correlation_id": "", "original_url": "https://example.com"}]`).
		Post(ts.URL + "/api/shorten/batch")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())

	// Невалидный батч: original_url не URL
	resp, err = resty.New().R().
		SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(`[{"correlation_id": "1", "original_url": "not-a-url"}]`).
		Post(ts.URL + "/api/shorten/batch")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())

	// Невалидный JSON
	resp, err = resty.New().R().
		SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(`[broken`).
		Post(ts.URL + "/api/shorten/batch")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())

	// Неверный Content-Type
	resp, err = resty.New().R().
		SetHeader(handler.ContentType, "text/xml").
		SetBody(`[]`).
		Post(ts.URL + "/api/shorten/batch")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())
}

func (s *RouterTestSuite) TestWebhook_getUserURLs() {
	ts, _ := s.newRealRouterServer(nil)

	// Клиент с cookie-jar: все запросы идут от одного пользователя
	cli := resty.New()
	cli.GetClient().Jar, _ = cookiejar.New(nil)

	// Два сокращения: новый URL — 201, ранее сохранённый другим пользователем — 409
	resp, err := cli.R().SetBody("http://user.list.one.io").Post(ts.URL + "/")
	s.Require().NoError(err)
	s.Equal(http.StatusCreated, resp.StatusCode())
	resp, err = cli.R().SetBody(yandexLongURL).Post(ts.URL + "/")
	s.Require().NoError(err)
	s.Equal(http.StatusConflict, resp.StatusCode())

	// У нового (пустого) пользователя список пуст
	anonCli := resty.New()
	anonCli.GetClient().Jar, _ = cookiejar.New(nil)
	resp, err = anonCli.R().Get(ts.URL + "/api/user/urls")
	s.Require().NoError(err)
	s.Equal(http.StatusNoContent, resp.StatusCode())

	// Пользователь с сокращёнными URL получает непустой список
	resp, err = cli.R().Get(ts.URL + "/api/user/urls")
	s.Require().NoError(err)
	s.Equal(http.StatusOK, resp.StatusCode())

	var got model.UserURLsRes
	s.Require().NoError(json.Unmarshal(resp.Body(), &got))
	s.NotEmpty(got)
	for _, item := range got {
		s.True(strings.HasPrefix(item.ShortURL, targetBaseURL+"/"))
		s.NotEmpty(item.OriginalURL)
	}
}

func (s *RouterTestSuite) TestWebhook_markUserURLsDeleted() {
	ts, _ := s.newRealRouterServer(nil)

	cli := resty.New()
	cli.GetClient().Jar, _ = cookiejar.New(nil)

	// Сохраняем URL и получаем его токен из ответа
	resp, err := cli.R().SetBody("http://to.delete.soon").Post(ts.URL + "/")
	s.Require().NoError(err)
	s.Equal(http.StatusCreated, resp.StatusCode())
	stored := strings.TrimPrefix(strings.TrimSpace(resp.String()), targetBaseURL+"/")
	s.NotEmpty(stored)

	// Сначала токен резолвится
	resp, err = resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R().Get(ts.URL + "/" + stored)
	s.Require().Condition(func() bool { return err == nil || isErrAutoRedirectDisabled(err) })
	s.Equal(http.StatusTemporaryRedirect, resp.StatusCode())

	// Удаляем
	body, err := json.Marshal(model.TokensToMarkDeleted{stored})
	s.Require().NoError(err)
	resp, err = cli.R().SetHeader(handler.ContentType, handler.AppJSON).SetBody(body).
		Delete(ts.URL + "/api/user/urls")
	s.Require().NoError(err)
	s.Equal(http.StatusAccepted, resp.StatusCode())

	// Удаление асинхронное: ждём, пока токен реально удалится
	s.Require().Eventually(func() bool {
		r, e := resty.New().R().Get(ts.URL + "/" + stored)
		if e != nil {
			return false
		}
		return r.StatusCode() == http.StatusGone
	}, 2*time.Second, 50*time.Millisecond, "токен должен удалиться")

	// Невалидный токен в запросе на удаление
	resp, err = cli.R().SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(`["abc$"]`).
		Delete(ts.URL + "/api/user/urls")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())

	// Невалидный JSON
	resp, err = cli.R().SetHeader(handler.ContentType, handler.AppJSON).
		SetBody(`[broken`).
		Delete(ts.URL + "/api/user/urls")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())

	// Неверный Content-Type
	resp, err = cli.R().SetHeader(handler.ContentType, "text/xml").
		SetBody(`[]`).
		Delete(ts.URL + "/api/user/urls")
	s.Require().NoError(err)
	s.Equal(http.StatusBadRequest, resp.StatusCode())
}

func (s *RouterTestSuite) TestWebhook_Get_Ping() {
	ts, _ := s.newRealRouterServer(nil)

	resp, err := resty.New().R().Get(ts.URL + "/ping")
	s.Require().NoError(err)
	s.Equal(http.StatusOK, resp.StatusCode())
}

// -----------------------------------------------------------------------------------------------
// Мок-тесты: точное управление поведением сервиса
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestShortenText_Mock() {
	tests := []struct {
		name    string
		expect  func(m *mocks.MockShortener)
		reqBody string
		erCode  int
		erBody  string
	}{
		{
			name: "успех",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
				m.EXPECT().GenerateAndStore(mock.Anything, "http://text.api", userID).Return("abc123", nil).Once()
			},
			reqBody: "http://text.api",
			erCode:  http.StatusCreated,
			erBody:  targetBaseURL + "/abc123",
		},
		{
			name: "конфликт по исходному URL",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
				// Сервис при конфликте URL возвращает (StoredToken, ErrOriginalURLExists):
				// токен хендлер берёт из возвращаемого значения, а не из ошибки
				m.EXPECT().GenerateAndStore(mock.Anything, yandexLongURL, userID).
					Return(yandexToken, service.NewErrOriginalURLExists(yandexToken, yandexLongURL)).Once()
			},
			reqBody: yandexLongURL,
			erCode:  http.StatusConflict,
			erBody:  targetBaseURL + "/" + yandexToken,
		},
		{
			name: "прочая ошибка сервиса",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
				m.EXPECT().GenerateAndStore(mock.Anything, "http://text.api", userID).
					Return("", fmt.Errorf("storage down")).Once()
			},
			reqBody: "http://text.api",
			erCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			m := s.newMockStorage()
			tc.expect(m)
			ts := s.newMockRouterServer(m, nil)

			resp, err := resty.New().R().SetBody(tc.reqBody).Post(ts.URL + "/")
			require.NoError(t, err)
			assert.Equal(t, tc.erCode, resp.StatusCode())
			if tc.erBody != "" {
				assert.Equal(t, tc.erBody, resp.String())
			}
			if tc.erCode == http.StatusCreated || tc.erCode == http.StatusConflict {
				assert.Equal(t, handler.TextPlain, resp.Header().Get(handler.ContentType))
			}
		})
	}
}

func (s *RouterTestSuite) TestShortenJSON_Mock() {
	tests := []struct {
		name   string
		body   string
		erCode int
	}{
		{name: "невалидный json", body: `{"broken`, erCode: http.StatusBadRequest},
		{name: "невалидная структура", body: `{"url": "not-a-url"}`, erCode: http.StatusBadRequest},
	}
	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			// Из вызовов сервиса ожидается только регистрация пользователя:
			// сам запрос до хендлера не доходит
			m := s.newMockStorage()
			m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
			ts := s.newMockRouterServer(m, nil)

			resp, err := s.postAPIShorten(ts, tc.body)
			require.NoError(t, err)
			assert.Equal(t, tc.erCode, resp.StatusCode())
		})
	}
}

func (s *RouterTestSuite) TestResolve_Mock() {
	tests := []struct {
		name       string
		token      string
		expect     func(m *mocks.MockShortener)
		erCode     int
		erLocation string
	}{
		{
			name:  "успех",
			token: "abc123XY",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().Resolve(mock.Anything, "abc123XY").Return("https://resolved.io", nil).Once()
			},
			erCode:     http.StatusTemporaryRedirect,
			erLocation: "https://resolved.io",
		},
		{
			name:  "токен не найден",
			token: "miss0ngT",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().Resolve(mock.Anything, "miss0ngT").
					Return("", service.NewErrTokenNotFound("miss0ngT")).Once()
			},
			erCode: http.StatusBadRequest,
		},
		{
			name:  "токен удалён",
			token: "gon0neTT",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().Resolve(mock.Anything, "gon0neTT").
					Return("", service.NewErrTokenIsDeleted("gon0neTT")).Once()
			},
			erCode: http.StatusGone,
		},
		{
			name:  "прочая ошибка сервиса",
			token: "dead1ock",
			expect: func(m *mocks.MockShortener) {
				m.EXPECT().Resolve(mock.Anything, "dead1ock").Return("", fmt.Errorf("boom")).Once()
			},
			erCode: http.StatusInternalServerError,
		},
		{
			name:   "невалидный токен — сервис не вызывается",
			token:  "not!valid",
			expect: func(m *mocks.MockShortener) {}, // пусто: любой вызов — паника
			erCode: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			m := s.newMockStorage()
			tc.expect(m)
			ts := s.newMockRouterServer(m, nil)

			resp, err := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R().
				Get(ts.URL + "/" + tc.token)
			require.Conditionf(t, func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
				"неожиданная ошибка запроса: %v", err)
			assert.Equal(t, tc.erCode, resp.StatusCode())
			if tc.erLocation != "" {
				assert.Equal(t, tc.erLocation, resp.Header().Get("Location"))
			}
		})
	}
}

func (s *RouterTestSuite) TestBatch_Mock() {
	s.T().Run("успех", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).RunAndReturn(
			func(ctx context.Context, batch model.BatchShortenReq, uid string) (model.BatchShortenRes, error) {
				res := make(model.BatchShortenRes, len(batch))
				for i, it := range batch {
					res[i] = model.BatchShortenResItem{CorrelationID: it.CorrelationID, ShortURL: fmt.Sprintf("tok%d", i)}
				}
				return res, nil
			}).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`[{"correlation_id":"1","original_url":"https://one.io"},{"correlation_id":"2","original_url":"https://two.io"}]`).
			Post(ts.URL + "/api/shorten/batch")
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Contains(t, resp.String(), targetBaseURL+"/tok0")
		assert.Contains(t, resp.String(), targetBaseURL+"/tok1")
	})

	s.T().Run("конфликт по URL", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).Return(
			model.BatchShortenRes{{CorrelationID: "1", ShortURL: yandexToken, ConflictedURL: true}}, nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`[{"correlation_id":"1","original_url":"` + yandexLongURL + `"}]`).
			Post(ts.URL + "/api/shorten/batch")
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, resp.StatusCode())
		assert.Contains(t, resp.String(), targetBaseURL+"/"+yandexToken)
	})

	s.T().Run("ошибка сервиса", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).Return(nil, fmt.Errorf("boom")).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`[{"correlation_id":"1","original_url":"https://one.io"}]`).
			Post(ts.URL + "/api/shorten/batch")
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	})

	s.T().Run("невалидный Content-Type — BatchStore не вызывается", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, "text/plain").
			SetBody(`[]`).
			Post(ts.URL + "/api/shorten/batch")
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})
}

func (s *RouterTestSuite) TestListUserURLs_Mock() {
	s.T().Run("непустой список", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().ListUserURLs(mock.Anything, userID).Return(
			model.UserURLsRes{
				{ShortURL: yandexToken, OriginalURL: yandexLongURL},
			}, nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().Get(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode())
		assert.Contains(t, resp.String(), targetBaseURL+"/"+yandexToken)
		assert.Contains(t, resp.String(), yandexLongURL)
	})

	s.T().Run("пустой список — 204", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().ListUserURLs(mock.Anything, userID).Return(model.UserURLsRes{}, nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().Get(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode())
		assert.Empty(t, resp.Body())
	})

	s.T().Run("ошибка сервиса", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().ListUserURLs(mock.Anything, userID).Return(nil, fmt.Errorf("boom")).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().Get(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	})
}

func (s *RouterTestSuite) TestMarkUserURLsDeleted_Mock() {
	s.T().Run("успех", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything, userID).
			Run(func(ctx context.Context, batch model.TokensToMarkDeleted, uid string) {
				assert.Equal(t, model.TokensToMarkDeleted{"tok12Ab"}, batch)
			}).Return(nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`["tok12Ab"]`).
			Delete(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode())
	})

	s.T().Run("ошибка постановки в очередь — 202 и добивка лога", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything, userID).
			Return(fmt.Errorf("очередь переполнена")).Once()
		m.EXPECT().HandleErrTokenLeftUndeletedAndLogData(mock.Anything).Return(nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`["tok12Ab"]`).
			Delete(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode())
	})

	s.T().Run("ошибка добивки лога не меняет код", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything, userID).
			Return(fmt.Errorf("очередь переполнена")).Once()
		m.EXPECT().HandleErrTokenLeftUndeletedAndLogData(mock.Anything).Return(fmt.Errorf("cannot write log")).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`["tok12Ab"]`).
			Delete(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode())
	})

	s.T().Run("невалидный токен — сервис не вызывается", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetBody(`["bad!token"]`).
			Delete(ts.URL + "/api/user/urls")
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})
}

func (s *RouterTestSuite) TestPing_Mock() {
	s.T().Run("успех", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().Ping(mock.Anything).Return(nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().Get(ts.URL + "/ping")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode())
	})

	s.T().Run("ошибка", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().Ping(mock.Anything).Return(fmt.Errorf("db down")).Once()
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().Get(ts.URL + "/ping")
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	})
}

// -----------------------------------------------------------------------------------------------
// CookieMiddleware на моке
// -----------------------------------------------------------------------------------------------

const apiShortenBody = `{"url": "https://middleware.check"}`

func (s *RouterTestSuite) postShortenWithCookie(ts *httptest.Server, jwtVal string) *resty.Response {
	req := resty.New().R().SetHeader(handler.ContentType, handler.AppJSON).SetBody(apiShortenBody)
	if jwtVal != "" {
		req.SetHeader("Cookie", MakeTestApplicationConfig().CookieUserIDName+"="+jwtVal)
	}
	resp, err := req.Post(ts.URL + "/api/shorten")
	s.Require().NoError(err)
	return resp
}

func (s *RouterTestSuite) TestMiddleware_NoCookie() {
	m := s.newMockStorage()
	m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
	m.EXPECT().GenerateAndStore(mock.Anything, "https://middleware.check", userID).Return("mid12Ab", nil).Once()
	ts := s.newMockRouterServer(m, nil)

	resp := s.postShortenWithCookie(ts, "")
	s.Equal(http.StatusCreated, resp.StatusCode())
	// Пользователю выдана новая cookie
	s.Contains(resp.Header().Get("Set-Cookie"), MakeTestApplicationConfig().CookieUserIDName)
}

func (s *RouterTestSuite) TestMiddleware_CreateUserError() {
	m := s.newMockStorage()
	m.EXPECT().CreateUser(mock.Anything).Return("", fmt.Errorf("storage down")).Once()
	ts := s.newMockRouterServer(m, nil)

	resp := s.postShortenWithCookie(ts, "")
	s.Equal(http.StatusInternalServerError, resp.StatusCode())
}

func (s *RouterTestSuite) TestMiddleware_InvalidCookie() {
	m := s.newMockStorage()
	m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
	m.EXPECT().GenerateAndStore(mock.Anything, "https://middleware.check", userID).Return("mid12Ab", nil).Once()
	ts := s.newMockRouterServer(m, nil)

	resp := s.postShortenWithCookie(ts, "это не JWT")
	s.Equal(http.StatusCreated, resp.StatusCode())
	s.Contains(resp.Header().Get("Set-Cookie"), MakeTestApplicationConfig().CookieUserIDName)
}

func (s *RouterTestSuite) TestMiddleware_EmptyIDInCookie() {
	m := s.newMockStorage() // сервис не должен вызываться
	ts := s.newMockRouterServer(m, nil)

	resp := s.postShortenWithCookie(ts, s.mustJWT(""))
	s.Equal(http.StatusUnauthorized, resp.StatusCode())
}

func (s *RouterTestSuite) TestMiddleware_CheckUserExists() {
	s.T().Run("пользователь существует", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CheckUserExists(mock.Anything, userID).Return(true, nil).Once()
		m.EXPECT().GenerateAndStore(mock.Anything, "https://middleware.check", userID).Return("mid12Ab", nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp := s.postShortenWithCookie(ts, s.mustJWT(userID))
		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		// Cookie не перевыдаётся: пользователь валиден
		assert.Empty(t, resp.Header().Get("Set-Cookie"))
	})

	s.T().Run("пользователь не существует", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CheckUserExists(mock.Anything, userID).Return(false, nil).Once()
		ts := s.newMockRouterServer(m, nil)

		resp := s.postShortenWithCookie(ts, s.mustJWT(userID))
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})

	s.T().Run("ошибка проверки", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CheckUserExists(mock.Anything, userID).Return(false, fmt.Errorf("storage down")).Once()
		ts := s.newMockRouterServer(m, nil)

		resp := s.postShortenWithCookie(ts, s.mustJWT(userID))
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	})
}

// -----------------------------------------------------------------------------------------------
// Аудит
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestAudit() {
	obs := &fakeNotifier{}
	ts, _ := s.newRealRouterServer(obs)

	// События сокращения: и на 201, и на 409
	_, err := resty.New().R().SetBody("http://audit.one.io").Post(ts.URL + "/")
	s.Require().NoError(err)
	_, err = resty.New().R().SetBody(yandexLongURL).Post(ts.URL + "/")
	s.Require().NoError(err)

	events := obs.snapshot()
	s.Require().Len(events, 2, "аудит отправляется и на 201, и на 409")
	for _, e := range events {
		s.Equal(audit.ActionShorten, e.Action)
		s.NotEmpty(e.UserID, "сокращение проходит через CookieMiddleware: user_id известен")
		s.NotEmpty(e.URL)
	}
	s.Equal("http://audit.one.io", events[0].URL)
	s.Equal(yandexLongURL, events[1].URL)

	// Follow-событие: GET /{token} вне CookieMiddleware
	_, err = resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R().Get(ts.URL + "/" + yandexToken)
	s.Require().Condition(func() bool { return err == nil || isErrAutoRedirectDisabled(err) })

	events = obs.snapshot()
	s.Require().Len(events, 3)
	s.Equal(audit.ActionFollow, events[2].Action)
	s.Empty(events[2].UserID, "у follow-события user_id пустой: запрос вне CookieMiddleware")
	s.Equal(yandexLongURL, events[2].URL)
}

func (s *RouterTestSuite) TestAudit_Disabled() {
	// Аудит отключён (nil): роутер работает без паники
	ts, _ := s.newRealRouterServer(nil)

	resp, err := resty.New().R().SetBody("http://no.audit.io").Post(ts.URL + "/")
	s.Require().NoError(err)
	s.Equal(http.StatusCreated, resp.StatusCode())
}

// -----------------------------------------------------------------------------------------------
// Gzip
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestGzipCompression() {
	// sends_gzip: клиент просит сжатие — успешный ответ приходит сжатым
	s.T().Run("sends_gzip", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().GenerateAndStore(mock.Anything, "https://gzip.check", userID).Return("gz12Ab", nil).Once()
		ts := s.newMockRouterServer(m, nil)

		// Сырой http-клиент: resty сам добавил бы Accept-Encoding и декомпрессировал ответ
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/", strings.NewReader("https://gzip.check"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Accept-Encoding", "gzip")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))

		zr, err := gzip.NewReader(resp.Body)
		require.NoError(t, err)
		decompressed, err := io.ReadAll(zr)
		require.NoError(t, err)
		assert.Equal(t, targetBaseURL+"/gz12Ab", string(decompressed))
	})

	// accepts_gzip: клиент шлёт gzip-тело — сервер распаковывает
	s.T().Run("accepts_gzip", func(t *testing.T) {
		m := s.newMockStorage()
		m.EXPECT().CreateUser(mock.Anything).Return(userID, nil).Once()
		m.EXPECT().GenerateAndStore(mock.Anything, "https://gzip.request", userID).Return("gz34Cd", nil).Once()
		ts := s.newMockRouterServer(m, nil)

		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, err := zw.Write([]byte(`{"url": "https://gzip.request"}`))
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetHeader("Content-Encoding", "gzip").
			SetBody(buf.Bytes()).
			Post(ts.URL + "/api/shorten")
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Contains(t, resp.String(), targetBaseURL+"/gz34Cd")
	})

	// Невалидное gzip-тело — 400 от middleware
	s.T().Run("невалидный gzip", func(t *testing.T) {
		m := s.newMockStorage() // сервис не должен вызываться
		ts := s.newMockRouterServer(m, nil)

		resp, err := resty.New().R().
			SetHeader(handler.ContentType, handler.AppJSON).
			SetHeader("Content-Encoding", "gzip").
			SetBody([]byte("это не gzip")).
			Post(ts.URL + "/api/shorten")
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})
}

// -----------------------------------------------------------------------------------------------
// Лимиты размера тел
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestBodyLimits() {
	m := s.newMockStorage()
	// CreateUser вызывается на каждый запрос; число не фиксируем
	m.EXPECT().CreateUser(mock.Anything).Return(userID, nil)
	ts := s.newMockRouterServer(m, nil)

	s.T().Run("текстовый URL длиннее 2048", func(t *testing.T) {
		resp, err := resty.New().R().SetBody(strings.Repeat("a", 3000)).Post(ts.URL + "/")
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("json длиннее 4096", func(t *testing.T) {
		resp, err := s.postAPIShorten(ts, `{"url": "http://x."+`+strings.Repeat("a", 5000)+`}`)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})
}

// -----------------------------------------------------------------------------------------------
// Прочее
// -----------------------------------------------------------------------------------------------

func (s *RouterTestSuite) TestNotFoundAndMethodNotAllowed() {
	m := s.newMockStorage()
	ts := s.newMockRouterServer(m, nil) // сервис не вызывается

	resp, err := resty.New().R().Get(ts.URL + "/token/deep/path")
	require.NoError(s.T(), err)
	assert.Equal(s.T(), http.StatusBadRequest, resp.StatusCode())
	assert.Contains(s.T(), resp.String(), "недопустимый путь в URL")

	resp, err = resty.New().R().Put(ts.URL + "/" + yandexToken)
	require.NoError(s.T(), err)
	assert.Equal(s.T(), http.StatusBadRequest, resp.StatusCode())
	assert.Contains(s.T(), resp.String(), "недопустимый метод")
}

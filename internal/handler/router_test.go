package handler_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RouterTestSuite struct {
	suite.Suite
	storage service.BasicStorage
}

func TestRouterTestSuite(t *testing.T) {
	suite.Run(t, new(RouterTestSuite))
}

var (
	userID        = "8222be97-0266-40d1-b069-54f29508de43"
	yandexToken   = "0123AbcD"
	yandexLongURL = "https://yandex.ru"
	deletedToken  = "QwertY23"
	secretAESKey  = []byte("amustbe32byteslongsecretkey!!!20")
	secretJWTKey  = []byte("your-jwt-signing-secret")
)

func (s *RouterTestSuite) SetupTest() {
	s.storage = repository.NewLocalStorage()
	if s.storage == nil {
		s.T().Fatal("Ошибка создания локального хранилища")
	}

	ctx := context.Background()

	if err := s.storage.Store(ctx, yandexToken, yandexLongURL, userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}

	url, err := s.storage.Resolve(ctx, yandexToken)
	if err != nil {
		s.T().Fatalf("Ошибка извлечения тестовых данных из хранилища: %v", err)
	}
	if url != yandexLongURL {
		s.T().Fatal("Ошибка подготовки тестовых данных")
	}

	if err := s.storage.Store(ctx, deletedToken, "http://to-be-deleted.com", userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}
	tdb := repository.ToMarkDeletedReqBatch{
		repository.ToMarkDeletedReqItem{
			Token:  deletedToken,
			UserID: userID,
		},
	}
	if err := s.storage.MarkUserURLsDeleted(ctx, tdb); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}
	_, err = s.storage.Resolve(ctx, deletedToken)
	s.Require().Error(err)
}

const targetBaseURL = "http://localhost:8080/test"

func MakeTestApplicationConfig() config.ApplicationConfig {
	svcConfig := service.DefaultShortenerConfig()

	return config.ApplicationConfig{
		TargetBaseURL:    targetBaseURL,
		ServiceConfig:    svcConfig,
		CookieUserIDName: "user_id_jwot",
		SigningKey:       secretJWTKey,
		CookieUserIDTTL:  24 * time.Hour,
	}
}

func (s *RouterTestSuite) TestWebhook() {
	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage = s.storage
	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		s.T().Fatalf("Ошибка создания тестового сервиса: %v", err)
	}
	rt, err := handler.NewRouter(handler.NewRouterParams(cfg, svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}
	ts := httptest.NewServer(rt)
	defer ts.Close()

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
			reqURL:    "/WasDΩ123",
			erResCode: http.StatusBadRequest,
		},
		{
			name:      "невалидный URL (содержит ASCII символы не из base62)",
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
			name:      "Токен ранее помечен удаленным",
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
			erResBody:        cfg.TargetBaseURL,
			erResHeader:      handler.ContentType,
			erResHeaderValue: handler.TextPlain,
			erResCode:        http.StatusCreated,
		},
		{
			name:             "валидные данные, но кофликт в исходном URL (запрос text/plain)",
			method:           http.MethodPost,
			reqURL:           "/",
			reqBody:          yandexLongURL,
			erResBody:        cfg.TargetBaseURL,
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
			// Учитывая формат ответа на запрос GET и особенности resty,
			// отключаем автоматическое следование за редиректами
			cli := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy())

			// Настройка тестового запроса
			req := cli.R()
			req.Method = tc.method
			req.URL = ts.URL + tc.reqURL
			req.Body = io.NopCloser(strings.NewReader(tc.reqBody))
			req.Header.Set("Accept-Encoding", "")

			resp, err := req.Send()

			// Проверяем возможные виды ошибок

			require.Conditionf(t,
				func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
				"ошибка при создании HTTP-запроса к серверу: %s", err,
			)

			assert.Equal(t, tc.erResCode, resp.StatusCode(), "Код ответа не совпадает с ожидаемым")

			if h := tc.erResHeader; h != "" {
				ehv := tc.erResHeaderValue
				if ahv := resp.Header().Get(h); ehv != ahv {
					t.Errorf("Заголовок %s: ожидаемое значение %q, фактическое значение %q", h, ehv, ahv)
				}
			}

			if tc.erResBody != "" {
				getBody := resp.Body
				require.NotNil(t, getBody)
				body := string(getBody())
				condition := strings.HasPrefix(body, tc.erResBody)
				assert.True(t, condition, "Тело ответа не совпадает с ожидаемым")
			}
		})
	}
}

func isErrAutoRedirectDisabled(err error) bool {
	return strings.HasSuffix(err.Error(), resty.ErrAutoRedirectDisabled.Error())
}

func (s *RouterTestSuite) TestWebhook_shortenLongURLForJSON() {
	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage = s.storage
	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		s.T().Fatalf("Ошибка создания тестового сервиса: %v", err)
	}
	rt, err := handler.NewRouter(handler.NewRouterParams(cfg, svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}
	ts := httptest.NewServer(rt)
	defer ts.Close()

	s.T().Run("валидные данные (запрос application/json)", func(t *testing.T) {
		body, err := json.Marshal(model.ShortenReq{URL: "http://another.good.url.ru"})
		require.NoError(t, err)
		resp := s.postAPIShorten(t, ts.URL, handler.AppJSON, body)
		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, handler.AppJSON, resp.Header().Get(handler.ContentType))
		require.NotNil(t, resp.Body)
		rb := io.NopCloser(bytes.NewReader(resp.Body()))
		var rbJSON model.ShortenRes
		err = json.NewDecoder(rb).Decode(&rbJSON)
		require.NoError(t, err)
		condition := strings.HasPrefix(rbJSON.Result, cfg.TargetBaseURL)
		assert.True(t, condition, "Тело ответа не совпадает с ожидаемым")
	})

	s.T().Run("валидные данные, но кофликт в исходном URL (запрос application/json)", func(t *testing.T) {
		body, err := json.Marshal(model.ShortenReq{URL: yandexLongURL})
		require.NoError(t, err)
		resp := s.postAPIShorten(t, ts.URL, handler.AppJSON, body)
		assert.Equal(t, http.StatusConflict, resp.StatusCode())
		assert.Equal(t, handler.AppJSON, resp.Header().Get(handler.ContentType))
		require.NotNil(t, resp.Body)
		rb := io.NopCloser(bytes.NewReader(resp.Body()))
		var rbJSON model.ShortenRes
		err = json.NewDecoder(rb).Decode(&rbJSON)
		require.NoError(t, err)
		condition := strings.HasPrefix(rbJSON.Result, cfg.TargetBaseURL)
		assert.True(t, condition, "Тело ответа не совпадает с ожидаемым")
	})

	s.T().Run("неверный Content Type", func(t *testing.T) {
		body, err := json.Marshal(model.ShortenReq{URL: yandexLongURL})
		require.NoError(t, err)
		resp := s.postAPIShorten(t, ts.URL, handler.TextPlain, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("невалидный json в теле запроса", func(t *testing.T) {
		resp := s.postAPIShorten(t, ts.URL, handler.AppJSON, []byte("{"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("нет поля url в теле запроса", func(t *testing.T) {
		resp := s.postAPIShorten(t, ts.URL, handler.AppJSON, []byte("{}"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("в теле запроса в поле url записан не URL", func(t *testing.T) {
		body, err := json.Marshal(model.ShortenReq{URL: "not-a-url"})
		require.NoError(t, err)
		resp := s.postAPIShorten(t, ts.URL, handler.AppJSON, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})
}

func (s *RouterTestSuite) postAPIShorten(t *testing.T, baseURL string, contentType string, body []byte) *resty.Response {
	req := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R()
	req.Method = http.MethodPost
	req.Header.Set(handler.ContentType, contentType)
	req.Header.Set("Accept-Encoding", "")
	req.URL = baseURL + "/api/shorten"
	req.Body = io.NopCloser(bytes.NewReader(body))

	resp, err := req.Send()
	require.Conditionf(t,
		func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
		"ошибка при создании HTTP-запроса к серверу: %s", err,
	)

	return resp
}

func (s *RouterTestSuite) TestWebhook_shortenBatch_Success() {
	batchReq := model.BatchShortenReq{
		model.BatchShortenReqItem{
			CorrelationID: "0",
			OriginalURL:   "http://example.one.com",
		},
		model.BatchShortenReqItem{
			CorrelationID: "1",
			OriginalURL:   "http://example.two.com",
		},
	}

	batchRes := model.BatchShortenRes{
		model.BatchShortenResItem{
			CorrelationID: batchReq[0].CorrelationID,
			ShortURL:      "tok4N0",
			ConflictedURL: false,
		},
		model.BatchShortenResItem{
			CorrelationID: batchReq[1].CorrelationID,
			ShortURL:      "t0k4N1",
			ConflictedURL: false,
		},
	}

	svc := new(service.MockShortener)

	sc := service.DefaultShortenerConfig()

	repoMock, connMock := newMockPgStorage(s.T())
	sc.Storage = repoMock

	svc.On("GetConfig").Return(sc)
	svc.On("BatchStore", mock.Anything, batchReq, userID).Return(batchRes, nil)

	// Успешное создание пользователя
	var userUIID pgtype.UUID
	require.NoError(s.T(), userUIID.Scan(userID))
	connMock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userUIID))

	rt, err := handler.NewRouter(handler.NewRouterParams(MakeTestApplicationConfig(), svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}

	ts := httptest.NewServer(rt)
	defer ts.Close()

	body, err := json.Marshal(batchReq)
	require.NoError(s.T(), err)

	req := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R()
	req.Method = http.MethodPost
	req.Header.Set(handler.ContentType, handler.AppJSON)
	req.Header.Set("Accept-Encoding", "")
	req.URL = ts.URL + "/api/shorten/batch"
	req.Body = io.NopCloser(bytes.NewReader(body))
	resp, err := req.Send()

	require.Conditionf(s.T(),
		func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
		"ошибка при создании HTTP-запроса к серверу: %s", err,
	)

	assert.Equal(s.T(), http.StatusCreated, resp.StatusCode())
	assert.Equal(s.T(), handler.AppJSON, resp.Header().Get(handler.ContentType))
	require.NotNil(s.T(), resp.Body)

	rb := io.NopCloser(bytes.NewReader(resp.Body()))
	var rbJSON model.BatchShortenRes
	err = json.NewDecoder(rb).Decode(&rbJSON)
	require.NoError(s.T(), err)
	require.Len(s.T(), rbJSON, len(batchRes))

	svc.AssertExpectations(s.T())
	assert.NoError(s.T(), connMock.ExpectationsWereMet())
}

func (s *RouterTestSuite) TestWebhook_shortenBatch_Success_With_Conflict_On_URL() {
	batchReq := model.BatchShortenReq{
		model.BatchShortenReqItem{
			CorrelationID: "0",
			OriginalURL:   "http://example.one.com",
		},
		model.BatchShortenReqItem{
			CorrelationID: "1",
			OriginalURL:   "http://example.two.com",
		},
	}

	batchRes := model.BatchShortenRes{
		model.BatchShortenResItem{
			CorrelationID: batchReq[0].CorrelationID,
			ShortURL:      "tok4N0",
			ConflictedURL: false,
		},
		model.BatchShortenResItem{
			CorrelationID: batchReq[1].CorrelationID,
			ShortURL:      "t0k4N1",
			ConflictedURL: true,
		},
	}

	svc := new(service.MockShortener)
	svc.On("BatchStore", mock.Anything, batchReq, userID).Return(batchRes, nil)

	sc := service.DefaultShortenerConfig()

	repoMock, connMock := newMockPgStorage(s.T())
	sc.Storage = repoMock

	svc.On("GetConfig").Return(sc)

	// Успешное создание пользователя
	var userUIID pgtype.UUID
	require.NoError(s.T(), userUIID.Scan(userID))
	connMock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userUIID))

	rt, err := handler.NewRouter(handler.NewRouterParams(MakeTestApplicationConfig(), svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}

	ts := httptest.NewServer(rt)
	defer ts.Close()

	body, err := json.Marshal(batchReq)
	require.NoError(s.T(), err)

	req := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R()
	req.Method = http.MethodPost
	req.Header.Set(handler.ContentType, handler.AppJSON)
	req.Header.Set("Accept-Encoding", "")
	req.URL = ts.URL + "/api/shorten/batch"
	req.Body = io.NopCloser(bytes.NewReader(body))
	resp, err := req.Send()

	require.Conditionf(s.T(),
		func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
		"ошибка при создании HTTP-запроса к серверу: %s", err,
	)

	assert.Equal(s.T(), http.StatusConflict, resp.StatusCode())
	assert.Equal(s.T(), handler.AppJSON, resp.Header().Get(handler.ContentType))
	require.NotNil(s.T(), resp.Body)

	rb := io.NopCloser(bytes.NewReader(resp.Body()))
	var rbJSON model.BatchShortenRes
	err = json.NewDecoder(rb).Decode(&rbJSON)
	require.NoError(s.T(), err)
	require.Len(s.T(), rbJSON, len(batchRes))

	svc.AssertExpectations(s.T())
	assert.NoError(s.T(), connMock.ExpectationsWereMet())
}

func (s *RouterTestSuite) TestGzipCompression() {
	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage = s.storage

	svc := new(service.MockShortener)
	svc.On("GetConfig").Return(cfg.ServiceConfig)
	svc.On("GenerateAndStore", mock.Anything, yandexLongURL, mock.Anything).Return(yandexToken, nil)

	rt, err := handler.NewRouter(handler.NewRouterParams(cfg, svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}

	ts := httptest.NewServer(rt)
	defer ts.Close()

	requestBody := fmt.Sprintf(`{"url": "%s"}`, yandexLongURL)
	successBody := fmt.Sprintf(`{"result": "%s/%s"}`, cfg.TargetBaseURL, yandexToken)

	s.T().Run("sends_gzip", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		zb := gzip.NewWriter(buf)
		_, err := zb.Write([]byte(requestBody))
		require.NoError(t, err)
		err = zb.Close()
		require.NoError(t, err)

		r := httptest.NewRequest("POST", ts.URL+"/api/shorten", buf)
		r.RequestURI = ""
		r.Header.Set(handler.ContentType, handler.AppJSON)
		r.Header.Set("Content-Encoding", "gzip")
		r.Header.Set("Accept-Encoding", "")

		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		defer resp.Body.Close()

		bd, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.JSONEq(t, successBody, string(bd))
	})

	s.T().Run("accepts_gzip", func(t *testing.T) {
		buf := bytes.NewBufferString(requestBody)
		r := httptest.NewRequest("POST", ts.URL+"/api/shorten", buf)
		r.RequestURI = ""
		r.Header.Set(handler.ContentType, handler.AppJSON)
		r.Header.Set("Accept-Encoding", "gzip")

		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		defer resp.Body.Close()

		zr, err := gzip.NewReader(resp.Body)
		require.NoError(t, err)

		bd, err := io.ReadAll(zr)
		require.NoError(t, err)
		require.JSONEq(t, successBody, string(bd))
	})

	svc.AssertExpectations(s.T())
}

func newMockPgStorage(t *testing.T) (service.BasicStorage, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	require.NoError(t, err)
	return repository.NewPgStorage(mock), mock
}

func (s *RouterTestSuite) TestWebhook_Get_Ping() {
	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage, _ = newMockPgStorage(s.T())

	svc := new(service.MockShortener)
	svc.On("GetConfig").Return(cfg.ServiceConfig)

	rt, err := handler.NewRouter(handler.NewRouterParams(cfg, svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}

	ts := httptest.NewServer(rt)
	defer ts.Close()

	s.T().Run("успешный ping БД", func(t *testing.T) {
		svc.On("Ping", mock.Anything).Return(nil).Once()

		req := resty.New().R()
		req.Method = http.MethodGet
		req.URL = ts.URL + "/ping"

		resp, err := req.Send()
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode())
	})

	s.T().Run("ошибка ping БД", func(t *testing.T) {
		svc.On("Ping", mock.Anything).Return(errors.New("connection timeout")).Once()

		req := resty.New().R()
		req.Method = http.MethodGet
		req.URL = ts.URL + "/ping"

		resp, err := req.Send()
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode())
	})

	svc.AssertExpectations(s.T())
}

func (s *RouterTestSuite) TestWebhook_getUserURLs() {
	repoMock, connMock := newMockPgStorage(s.T())

	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage = repoMock

	var userUIID pgtype.UUID
	require.NoError(s.T(), userUIID.Scan(userID))

	svc := new(service.MockShortener)
	svc.On("GetConfig").Return(cfg.ServiceConfig)

	rt, err := handler.NewRouter(handler.NewRouterParams(cfg, svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}

	ts := httptest.NewServer(rt)
	defer ts.Close()

	s.T().Run("код 204", func(t *testing.T) {
		connMock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userUIID)).
			Times(1)

		svc.On("ListUserURLs", mock.Anything, userID).Return(model.UserURLsRes{}, nil).Once()

		req := resty.New().R()
		req.Method = http.MethodGet
		req.URL = ts.URL + "/api/user/urls"

		resp, err := req.Send()
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode())
	})

	s.T().Run("код 200", func(t *testing.T) {
		connMock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userUIID)).
			Times(1)

		resURLs := model.UserURLsRes{
			model.UserURLsResItem{
				ShortURL:    yandexToken,
				OriginalURL: yandexLongURL,
			},
		}
		svc.On("ListUserURLs", mock.Anything, userID).Return(resURLs, nil).Once()

		req := resty.New().R()
		req.Method = http.MethodGet
		req.URL = ts.URL + "/api/user/urls"

		resp, err := req.Send()
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode())

		assert.Equal(t, handler.AppJSON, resp.Header().Get(handler.ContentType))
		require.NotNil(t, resp.Body)

		rb := io.NopCloser(bytes.NewReader(resp.Body()))
		var rbJSON model.UserURLsRes
		err = json.NewDecoder(rb).Decode(&rbJSON)
		require.NoError(t, err)
		require.Len(t, rbJSON, len(resURLs))
	})

	svc.AssertExpectations(s.T())
	assert.NoError(s.T(), connMock.ExpectationsWereMet())
}

func (s *RouterTestSuite) TestWebhook_markUserURLsDeleted() {
	batchReq := model.TokensToMarkDeleted{yandexToken}

	svc := new(service.MockShortener)
	svc.On("MarkUserURLsDeleted", mock.Anything, batchReq, userID).Return(nil)

	sc := service.DefaultShortenerConfig()

	repoMock, connMock := newMockPgStorage(s.T())
	sc.Storage = repoMock

	svc.On("GetConfig").Return(sc)

	// Успешное создание пользователя
	var userUIID pgtype.UUID
	require.NoError(s.T(), userUIID.Scan(userID))
	connMock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userUIID))

	rt, err := handler.NewRouter(handler.NewRouterParams(MakeTestApplicationConfig(), svc))
	if err != nil {
		s.T().Fatalf("Ошибка настройки тестового маршрутизатора запросов: %v", err)
	}

	ts := httptest.NewServer(rt)
	defer ts.Close()

	body, err := json.Marshal(batchReq)
	require.NoError(s.T(), err)

	req := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R()
	req.Method = http.MethodDelete
	req.Header.Set(handler.ContentType, handler.AppJSON)
	req.Header.Set("Accept-Encoding", "")
	req.URL = ts.URL + "/api/user/urls"
	req.Body = io.NopCloser(bytes.NewReader(body))
	resp, err := req.Send()

	require.Conditionf(s.T(),
		func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
		"ошибка при создании HTTP-запроса к серверу: %s", err,
	)

	assert.Equal(s.T(), http.StatusAccepted, resp.StatusCode())
	assert.Equal(s.T(), handler.AppJSON, resp.Header().Get(handler.ContentType))

	svc.AssertExpectations(s.T())
	assert.NoError(s.T(), connMock.ExpectationsWereMet())
}

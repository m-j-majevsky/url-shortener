package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-resty/resty/v2"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RouterTestSuite struct {
	suite.Suite
	storage service.URLStorage
}

func TestRouterTestSuite(t *testing.T) {
	suite.Run(t, new(RouterTestSuite))
}

var (
	yandexToken   = "0123AbcD"
	yandexLongURL = "https://yandex.ru"
)

func (s *RouterTestSuite) SetupTest() {
	s.storage = repository.NewStorage()
	if s.storage == nil {
		s.T().Fatal("Ошибка создания хранилища")
	}

	if err := s.storage.Store(yandexToken, model.NewURL(yandexLongURL)); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %v", err)
	}

	url, ok := s.storage.Resolve(yandexToken)
	if !ok {
		s.T().Fatal("Ошибка извлечения тестовых данных из хранилища")
	}
	if url.String() != yandexLongURL {
		s.T().Fatal("Ошибка подготовки тестовых данных")
	}
}

const targetBaseURL = "http://localhost:8080/test"

func MakeTestApplicationConfig() config.ApplicationConfig {
	svcConfig := service.DefaultShortenerConfig()

	return config.ApplicationConfig{
		TargetBaseURL: targetBaseURL,
		ServiceConfig: svcConfig,
	}
}

func (s *RouterTestSuite) TestWebhook() {
	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage = s.storage
	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		s.T().Fatalf("Ошибка создания тестового сервиса: %v", err)
	}
	rt := handler.NewRouter(svc, cfg.TargetBaseURL)
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
			reqBody:          yandexLongURL,
			erResBody:        cfg.TargetBaseURL,
			erResHeader:      "Content-Type",
			erResHeaderValue: "text/plain",
			erResCode:        http.StatusCreated,
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
				condition := strings.HasPrefix(string(getBody()), tc.erResBody)
				assert.True(t, condition, "Тело ответа не совпадает с ожидаемым")
			}
		})
	}
}

func isErrAutoRedirectDisabled(err error) bool {
	return strings.HasSuffix(err.Error(), resty.ErrAutoRedirectDisabled.Error())
}

func (s *RouterTestSuite) TestWebhook_shortenLongURLForJson() {
	cfg := MakeTestApplicationConfig()
	cfg.ServiceConfig.Storage = s.storage
	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		s.T().Fatalf("Ошибка создания тестового сервиса: %v", err)
	}
	rt := handler.NewRouter(svc, cfg.TargetBaseURL)
	ts := httptest.NewServer(rt)
	defer ts.Close()

	s.T().Run("валидные данные (запрос application/json)", func(t *testing.T) {
		body, err := json.Marshal(model.PostApiShortenReq{URL: yandexLongURL})
		require.NoError(t, err)
		resp := s.postApiShorten(t, ts.URL, "application/json", body)
		assert.Equal(t, http.StatusCreated, resp.StatusCode())
		assert.Equal(t, "application/json", resp.Header().Get("Content-Type"))
		require.NotNil(t, resp.Body)
		rb := io.NopCloser(bytes.NewReader(resp.Body()))
		var rbJson model.PostApiShortenRes
		err = json.NewDecoder(rb).Decode(&rbJson)
		require.NoError(t, err)
		condition := strings.HasPrefix(rbJson.Result, cfg.TargetBaseURL)
		assert.True(t, condition, "Тело ответа не совпадает с ожидаемым")
	})

	s.T().Run("неверный Content Type", func(t *testing.T) {
		body, err := json.Marshal(model.PostApiShortenReq{URL: yandexLongURL})
		require.NoError(t, err)
		resp := s.postApiShorten(t, ts.URL, "text/plain", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("невалидный json в теле запроса", func(t *testing.T) {
		resp := s.postApiShorten(t, ts.URL, "application/json", []byte("{"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("нет поля url в теле запроса", func(t *testing.T) {
		resp := s.postApiShorten(t, ts.URL, "application/json", []byte("{}"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})

	s.T().Run("в теле запроса в поле url записан не URL", func(t *testing.T) {
		body, err := json.Marshal(model.PostApiShortenReq{URL: "not-a-url"})
		require.NoError(t, err)
		resp := s.postApiShorten(t, ts.URL, "application/json", body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode())
	})
}

func (s *RouterTestSuite) postApiShorten(t *testing.T, baseURL string, contentType string, body []byte) *resty.Response {
	req := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy()).R()
	req.Method = http.MethodPost
	req.Header.Set("Content-Type", contentType)
	req.URL = baseURL + "/api/shorten"
	req.Body = io.NopCloser(bytes.NewReader(body))

	resp, err := req.Send()
	require.Conditionf(t,
		func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
		"ошибка при создании HTTP-запроса к серверу: %s", err,
	)

	return resp
}

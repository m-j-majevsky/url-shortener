package handler_test

import (
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RouterTestSuite struct {
	suite.Suite
	storage repository.URLStorage
}

func TestRouterTestSuite(t *testing.T) {
	suite.Run(t, new(RouterTestSuite))
}

var (
	yandexShortURL string
	yandexLongURL  = "https://yandex.ru"
)

func (s *RouterTestSuite) SetupSuite() {
	s.storage = repository.NewURLStorage(0)
	res, err := s.storage.ShortenAndStore(model.NewLongURL(yandexLongURL))
	if err != nil {
		s.T().Fatal("Ошибка подготовки тестовых данных")
	}
	yandexShortURL = res.String()
	db := s.storage

	longURL, ok := db.Resolve(model.NewShortURL(yandexShortURL))
	if !ok {
		s.T().Fatal("Ошибка извлечения тестовых данных")
	}
	if longURL.String() != yandexLongURL {
		s.T().Fatal("Неверные тестовые данные")
	}
}

func configureTestApplication(s *RouterTestSuite) config.ApplicationConfig {
	return config.ApplicationConfig{
		TargetURLBase: "http://localhost:8111/url",
		Storage:       s.storage,
	}
}

func (suite *RouterTestSuite) TestWebhook() {
	cfg := configureTestApplication(suite)
	ctx := handler.NewRouterContext(cfg.Storage, cfg.TargetURLBase)
	ts := httptest.NewServer(ctx)
	defer ts.Close()

	testCases := []struct {
		name                string
		method              string
		requestURL          string
		requestBody         string
		expectedBody        string
		expectedHeader      string
		expectedHeaderValue string
		expectedCode        int
	}{
		// GET
		{
			name:         "невалидный URL (корень)",
			method:       http.MethodGet,
			requestURL:   "/",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "невалидный URL (содержит не ASCII символы)",
			method:       http.MethodGet,
			requestURL:   "/WasDΩ123",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "невалидный URL (содержит ASCII символы не из base62)",
			method:       http.MethodGet,
			requestURL:   "/WasD01_3",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "невалидный путь в URL",
			method:       http.MethodGet,
			requestURL:   "/token/WasD01_3",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "валидный URL, но данные не найдены",
			method:       http.MethodGet,
			requestURL:   "/WasD0123",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:                "валидный URL, и данные найдены",
			method:              http.MethodGet,
			requestURL:          "/" + yandexShortURL,
			expectedHeader:      "Location",
			expectedHeaderValue: yandexLongURL,
			expectedCode:        http.StatusTemporaryRedirect,
		},
		// POST
		{
			name:         "невалидный URL (любой, отличный от корня)",
			method:       http.MethodPost,
			requestURL:   "/badURL",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:         "пустое тело запроса недопустимо",
			method:       http.MethodPost,
			requestURL:   "/",
			requestBody:  "",
			expectedCode: http.StatusBadRequest,
		},
		{
			name:                "валидные данные",
			method:              http.MethodPost,
			requestURL:          "/",
			requestBody:         yandexLongURL,
			expectedBody:        cfg.TargetURLBase,
			expectedHeader:      "Content-Type",
			expectedHeaderValue: "text/plain",
			expectedCode:        http.StatusCreated,
		},
		// DELETE
		{
			name:         "невалидный метод",
			method:       http.MethodDelete,
			requestURL:   "/WasD0123",
			expectedCode: http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		suite.T().Run(fmt.Sprintf("%q: %s", tc.method, tc.name), func(t *testing.T) {
			// Учитывая формат ответа на запрос GET и особенности resty,
			// отключаем автоматическое следование за редиректами
			cli := resty.New().SetRedirectPolicy(resty.NoRedirectPolicy())

			// Настройка тестового запроса
			req := cli.R()
			req.Method = tc.method
			req.URL = ts.URL + tc.requestURL
			req.Body = io.NopCloser(strings.NewReader(tc.requestBody))

			resp, err := req.Send()

			// Проверяем возможные виды ошибок

			require.Conditionf(t,
				func() bool { return err == nil || isErrAutoRedirectDisabled(err) },
				"ошибка при создании HTTP-запроса к серверу: %s", err,
			)

			assert.Equal(t, tc.expectedCode, resp.StatusCode(), "Код ответа не совпадает с ожидаемым")

			if h := tc.expectedHeader; h != "" {
				ehv := tc.expectedHeaderValue
				if ahv := resp.Header().Get(h); ehv != ahv {
					t.Errorf("Заголовок %s: ожидаемое значение %q, фактическое значение %q", h, ehv, ahv)
				}
			}

			if tc.expectedBody != "" {
				getBody := resp.Body
				require.NotNil(t, getBody)
				condition := strings.HasPrefix(string(getBody()), tc.expectedBody)
				assert.True(t, condition, "Тело ответа не совпадает с ожидаемым")
			}
		})
	}
}

func isErrAutoRedirectDisabled(err error) bool {
	return strings.HasSuffix(err.Error(), resty.ErrAutoRedirectDisabled.Error())
}

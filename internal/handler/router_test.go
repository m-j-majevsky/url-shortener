package handler_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RouterTestSuite struct {
	suite.Suite
	storage model.URLStorage
}

func TestRouterTestSuite(t *testing.T) {
	suite.Run(t, new(RouterTestSuite))
}

const (
	yandexShortURL = "ZcVp01GT"
	yandexLongURL  = "https://yandex.ru"

	// expectedShortURLLength = 8
)

func (suite *RouterTestSuite) SetupSuite() {
	suite.storage = repository.ProvideURLStorage()
	db := suite.storage

	yandexShortURL := model.ShortURL(yandexShortURL)
	yandexLongURL := model.LongURL(yandexLongURL)

	if longURL, err := db.Get(yandexShortURL); err == nil && longURL == yandexLongURL {
		// Валидные тестовые данные уже присутствуют, можно работать
		return
	}

	if err := db.Add(yandexShortURL, yandexLongURL); err != nil {
		suite.T().Fatal("Невозможно добавить тестовые данные")
	}

	longURL, err := db.Get(yandexShortURL)
	if err != nil {
		suite.T().Fatal("Ошибка извлечения тестовых данных")
	}
	if longURL != yandexLongURL {
		suite.T().Fatal("Неверные тестовые данные")
	}
}

func (suite *RouterTestSuite) TestWebhook() {
	webhook := handler.CreateWebhook(suite.storage)
	localhost := "localhost:8080"
	// описываем набор данных: метод запроса, ожидаемый код ответа, ожидаемое тело
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
			name:         "валидные данные",
			method:       http.MethodPost,
			requestURL:   "/",
			requestBody:  yandexLongURL,
			expectedBody: fmt.Sprintf("http://%s/%s", localhost, yandexShortURL),
			expectedCode: http.StatusCreated,
		},
	}

	for _, tc := range testCases {
		suite.T().Run(fmt.Sprintf("%q: %s", tc.method, tc.name), func(t *testing.T) {
			b := strings.NewReader(tc.requestBody)
			r := httptest.NewRequest(tc.method, tc.requestURL, io.NopCloser(b))
			r.Host = localhost
			w := httptest.NewRecorder()
			webhook(w, r)

			result := w.Result()

			assert.Equal(t, tc.expectedCode, result.StatusCode, "Код ответа не совпадает с ожидаемым")

			if h := tc.expectedHeader; h != "" {
				ehv := tc.expectedHeaderValue
				if ahv := result.Header.Get(h); ehv != ahv {
					t.Errorf("Заголовок %s: ожидаемое значение %q, фактическое значение %q", h, ehv, ahv)
				}
			}

			if tc.expectedBody != "" {
				defer result.Body.Close()
				resultBody, err := io.ReadAll(result.Body)
				require.NoError(t, err)
				assert.Equal(t, tc.expectedBody, string(resultBody), "Тело ответа не совпадает с ожидаемым")
			}
		})
	}
}

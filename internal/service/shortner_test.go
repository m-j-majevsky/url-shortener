package service_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

var (
	yandexShortURL string
	yandexLongURL  = "https://yandex.ru"

	mailLongURL = "https://mail.ru"
	goLongURL   = "https://go.dev"

	aShortURL = "1Z2Z3Z5Z"
)

type ShortenerTestSuite struct {
	suite.Suite
	storage repository.URLStorage
}

func TestShortenerTestSuite(t *testing.T) {
	suite.Run(t, new(ShortenerTestSuite))
}

func (s *ShortenerTestSuite) SetupTest() {
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

func (suite *ShortenerTestSuite) TestShortenURLHappyPath() {
	shortURL, err := service.ShortenURL(suite.storage, mailLongURL)
	suite.Require().NoError(err)
	suite.NotEqual(shortURL, model.EmptyShortURL)
	suite.NoError(base62.ValidateBase62(shortURL))
	suite.Equal(len(shortURL), base62.TokenLength)
}

func (suite *ShortenerTestSuite) TestResolveExistingShortURL() {
	longURL, found := service.ResolveShortURL(suite.storage, yandexShortURL)
	suite.Require().True(found)
	suite.Equal(longURL, yandexLongURL)
}

func (suite *ShortenerTestSuite) TestResolveNotExistingShortURL() {
	longURL, found := service.ResolveShortURL(suite.storage, aShortURL)
	suite.Require().False(found)
	suite.Equal(longURL, "")
}

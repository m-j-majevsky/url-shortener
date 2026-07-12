package service_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
)

const (
	yandexShortURL = model.ShortURL("ZcVp01GT")
	yandexLongURL  = model.LongURL("https://yandex.ru")

	mailLongURL = model.LongURL("https://mail.ru")
	goLongURL   = model.LongURL("https://go.dev")

	aShortURL = model.ShortURL("1Z2Z3Z5Z")
)

type ShortenerTestSuite struct {
	suite.Suite
	storage model.URLStorage
}

func TestShortenerTestSuite(t *testing.T) {
	suite.Run(t, new(ShortenerTestSuite))
}

func (suite *ShortenerTestSuite) SetupSuite() {
	suite.storage = repository.ProvideURLStorage()
	db := suite.storage

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

func (suite *ShortenerTestSuite) TestShortenningAlreadyShortenURL() {
	shortURL, err := service.ShortenURL(suite.storage, yandexLongURL)
	suite.Require().Nil(err)
	suite.Equal(shortURL, yandexShortURL)
}

func (suite *ShortenerTestSuite) TestShortenURLWithNoErrorForValidInput() {
	shortURL, err := service.ShortenURL(suite.storage, mailLongURL)
	suite.Require().Nil(err)
	suite.NotEqual(shortURL, model.EmptyShortURL)
}

func (suite *ShortenerTestSuite) TestShortenURLResultTokenValidity() {
	shortURL, err := service.ShortenURL(suite.storage, goLongURL)
	suite.Require().Nil(err)
	suite.Require().Equal(service.TokenLength, len(shortURL))
	suite.Nil(base62.ValidateBase62(string(shortURL)))
}

func (suite *ShortenerTestSuite) TestResolveExistingShortURL() {
	longURL, err := service.ResolveShortURL(suite.storage, yandexShortURL)
	suite.Require().Nil(err)
	suite.Equal(longURL, yandexLongURL)
}

func (suite *ShortenerTestSuite) TestResolveNotExistingShortURL() {
	longURL, err := service.ResolveShortURL(suite.storage, aShortURL)
	suite.Require().NotNil(err)
	suite.Equal(longURL, model.EmptyLongURL)
}

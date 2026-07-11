package repository_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

type MapBasedDBTestSuite struct {
	suite.Suite
	storage model.URLStorage
}

const (
	yandexShortURL = model.ShortURL("ZcVp01GT")
	yandexLongURL  = model.LongURL("https://yandex.ru")

	mailShortURL = model.ShortURL("A1Qd8pEs")
	mailLongURL  = model.LongURL("https://mail.ru")

	goLongURL = model.LongURL("https://go.dev")

	someShortURL = model.ShortURL("0000ZZZZ")
)

func (suite *MapBasedDBTestSuite) SetupSuite() {
	suite.storage = repository.ProvideURLStorage()
	suite.storage.Add(yandexShortURL, yandexLongURL)
	if url, err := suite.storage.Get(yandexShortURL); err != nil || url != yandexLongURL {
		suite.T().Fatal("Ошибка подготовки тестовых данных")
	}
}

func (suite *MapBasedDBTestSuite) TestGetValueNotFound() {
	longURL, err := suite.storage.Get(someShortURL)
	suite.NotNil(err)
	suite.Equal(longURL, model.EmptyLongURL)
}

func (suite *MapBasedDBTestSuite) TestGetExistingValue() {
	longURL, err := suite.storage.Get(yandexShortURL)
	suite.Nil(err)
	suite.Equal(longURL, yandexLongURL)
}

func (suite *MapBasedDBTestSuite) TestAddValue() {
	_, err := suite.storage.Get(mailShortURL)
	suite.Require().NotNil(err)

	err = suite.storage.Add(mailShortURL, mailLongURL)
	suite.Require().Nil(err)

	url, err := suite.storage.Get(mailShortURL)
	suite.Equal(url, mailLongURL)
}

func (suite *MapBasedDBTestSuite) TestFindNotPresentValue() {
	shortURL, found := suite.storage.Find(goLongURL)
	suite.False(found)
	suite.Equal(shortURL, model.EmptyShortURL)
}

func (suite *MapBasedDBTestSuite) TestFindExistingValue() {
	shortURL, found := suite.storage.Find(yandexLongURL)
	suite.True(found)
	suite.Equal(shortURL, yandexShortURL)
}

func TestMapBasedDBTestSuite(t *testing.T) {
	suite.Run(t, new(MapBasedDBTestSuite))
}

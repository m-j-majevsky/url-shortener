package repository

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type MapBasedDBTestSuite struct {
	suite.Suite
	storage URLStorage
}

var (
	yandexShortURL model.ShortURL
	yandexLongURL  = model.NewLongURL("https://yandex.ru")

	mailLongURL = model.NewLongURL("https://mail.ru")

	goLongURL = model.NewLongURL("https://go.dev")

	someShortURL = model.NewShortURL("0000ZZZZ")
)

func (s *MapBasedDBTestSuite) SetupTest() {
	s.storage = NewURLStorage(0)
	res, err := s.storage.ShortenAndStore(yandexLongURL)
	if err != nil {
		s.T().Fatal("Ошибка подготовки тестовых данных")
	}
	yandexShortURL = res
}

func (suite *MapBasedDBTestSuite) TestGetValueNotFound() {
	longURL, ok := suite.storage.Resolve(someShortURL)
	suite.False(ok)
	suite.Equal(longURL, model.EmptyLongURL)
}

func (suite *MapBasedDBTestSuite) TestGetExistingValue() {
	longURL, ok := suite.storage.Resolve(yandexShortURL)
	suite.True(ok)
	suite.Equal(longURL, yandexLongURL)
}

func (suite *MapBasedDBTestSuite) TestAddValue() {
	_, ok := suite.storage.find(mailLongURL)
	suite.Require().False(ok)

	mailShortURL, err := suite.storage.ShortenAndStore(mailLongURL)
	suite.Require().NoError(err)
	suite.Require().NoError(base62.ValidateBase62(mailShortURL.String()))

	url, ok := suite.storage.Resolve(mailShortURL)
	suite.Require().True(ok)
	suite.Equal(url, mailLongURL)
}

func (suite *MapBasedDBTestSuite) TestFindNotPresentValue() {
	shortURL, found := suite.storage.find(goLongURL)
	suite.False(found)
	suite.Equal(shortURL, model.EmptyShortURL)
}

func (suite *MapBasedDBTestSuite) TestFindExistingValue() {
	shortURL, found := suite.storage.find(yandexLongURL)
	suite.True(found)
	suite.Equal(shortURL, yandexShortURL)
}

func TestMapBasedDBTestSuite(t *testing.T) {
	suite.Run(t, new(MapBasedDBTestSuite))
}

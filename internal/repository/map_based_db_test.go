package repository

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type MapBasedDBTestSuite struct {
	suite.Suite
	storage *Storage
}

var (
	yandexToken = "sPv80uUs"
	yandexURL   = model.NewURL("https://yandex.ru")

	mailToken = "9aF7e72i"
	mailURL   = model.NewURL("https://mail.ru")

	goURL = model.NewURL("https://go.dev")

	someToken = "0000ZZZZ"
)

func (s *MapBasedDBTestSuite) SetupTest() {
	s.storage = NewStorage()
	if err := s.storage.Store(yandexToken, yandexURL); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

func (suite *MapBasedDBTestSuite) TestUnresolvedToken() {
	url, ok := suite.storage.Resolve(someToken)
	suite.False(ok)
	suite.Equal(model.EmptyURL, url)
}

func (suite *MapBasedDBTestSuite) TestResolveSuccess() {
	url, ok := suite.storage.Resolve(yandexToken)
	suite.True(ok)
	suite.Equal(yandexURL, url)
}

func (suite *MapBasedDBTestSuite) TestStoreWithErrTokenTaken() {
	err := suite.storage.Store(yandexToken, goURL)
	suite.Require().Error(err)
	var errTT *ErrTokenTaken
	suite.ErrorAs(err, &errTT)
	suite.Equal(yandexToken, errTT.Token)
	suite.Contains(errTT.Error(), "занят")
}

func (suite *MapBasedDBTestSuite) TestStore() {
	err := suite.storage.Store(mailToken, mailURL)
	suite.Require().NoError(err)

	url, ok := suite.storage.Resolve(mailToken)
	suite.Require().True(ok)
	suite.Equal(mailURL, url)
}

func TestMapBasedDBTestSuite(t *testing.T) {
	suite.Run(t, new(MapBasedDBTestSuite))
}

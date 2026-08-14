package repository

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type MapBasedDBTestSuite struct {
	suite.Suite
	storage *LocalStorage
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
	s.storage = NewLocalStorage()
	if err := s.storage.Store(context.Background(), yandexToken, yandexURL); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

func (suite *MapBasedDBTestSuite) TestUnresolvedToken() {
	url, err := suite.storage.Resolve(context.Background(), someToken)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)
	suite.Equal(model.EmptyURL, url)
}

func (suite *MapBasedDBTestSuite) TestResolveSuccess() {
	url, err := suite.storage.Resolve(context.Background(), yandexToken)
	suite.NoError(err)
	suite.Equal(yandexURL, url)
}

func (suite *MapBasedDBTestSuite) TestStoreWithErrTokensTaken() {
	err := suite.storage.Store(context.Background(), yandexToken, goURL)
	suite.Require().Error(err)
	var errTT *ErrTokensTaken
	suite.ErrorAs(err, &errTT)
	suite.Equal(yandexToken, errTT.Tokens[0])
}

func (suite *MapBasedDBTestSuite) TestStore() {
	ctx := context.Background()

	errStore := suite.storage.Store(ctx, mailToken, mailURL)
	suite.Require().NoError(errStore)

	url, errResolse := suite.storage.Resolve(ctx, mailToken)
	suite.Require().NoError(errResolse)
	suite.Equal(mailURL, url)
}

func TestMapBasedDBTestSuite(t *testing.T) {
	suite.Run(t, new(MapBasedDBTestSuite))
}

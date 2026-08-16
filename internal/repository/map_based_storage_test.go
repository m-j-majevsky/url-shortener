package repository

import (
	"context"
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/stretchr/testify/suite"
)

type MapBasedDBTestSuite struct {
	suite.Suite
	storage *LocalStorage
}

var (
	yandexToken = "sPv80uUs"
	yandexURL   = "https://yandex.ru"

	mailToken = "9aF7e72i"
	mailURL   = "https://mail.ru"

	goToken = "GOTOKEN"
	goURL   = "https://go.dev"

	someToken = "0000ZZZZ"
	someURL   = "https://some.url.ru"
)

func (s *MapBasedDBTestSuite) SetupTest() {
	s.storage = NewLocalStorage()
	if err := s.storage.Store(context.Background(), yandexToken, yandexURL); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

func TestMapBasedDBTestSuite(t *testing.T) {
	suite.Run(t, new(MapBasedDBTestSuite))
}

// Resolve

func (suite *MapBasedDBTestSuite) TestResolve_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	url, err := suite.storage.Resolve(context.Background(), yandexToken)
	suite.NoError(err)
	suite.Equal(yandexURL, url)
}

func (suite *MapBasedDBTestSuite) TestResolve_ErrTokenNotFound() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	url, err := suite.storage.Resolve(context.Background(), someToken)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)
	suite.Equal("", url)
}

// Store

func (suite *MapBasedDBTestSuite) TestStore_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	errStore := suite.storage.Store(ctx, mailToken, mailURL)
	suite.Require().NoError(errStore)

	url, errResolse := suite.storage.Resolve(ctx, mailToken)
	suite.Require().NoError(errResolse)
	suite.Equal(mailURL, url)

	url, errResolse = suite.storage.Resolve(ctx, yandexToken)
	suite.Require().NoError(errResolse)
	suite.Equal(yandexURL, url)
}

func (suite *MapBasedDBTestSuite) TestStore_ErrTokenTaken() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	err := suite.storage.Store(context.Background(), yandexToken, goURL)
	suite.Require().Error(err)
	var errTT *ErrTokenTaken
	suite.ErrorAs(err, &errTT)
	suite.Equal(yandexToken, errTT.Token)
}

func (suite *MapBasedDBTestSuite) TestStore_ErrOriginalURLExists() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	err := suite.storage.Store(context.Background(), someToken, yandexURL)
	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexURL, eoue.URL)
}

func (suite *MapBasedDBTestSuite) TestStore_ErrOriginalURLExists_While_Token_Taken_Either() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	err := suite.storage.Store(context.Background(), yandexToken, yandexURL)
	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexURL, eoue.URL)
}

// BatchStore

func (suite *MapBasedDBTestSuite) TestBatchStore_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewBatch(model.BatchShortenReq{})

	// Пустой батч
	res, err := suite.storage.BatchStore(ctx, batch)
	suite.Require().NoError(err)
	suite.Assert().Empty(res)

	// Укладка единственного элемента
	batch = append(batch, BatchItem{
		CorrelationID: "1",
		Token:         mailToken,
		OriginalURL:   mailURL,
	})
	res, err = suite.storage.BatchStore(ctx, batch)
	suite.Require().NoError(err)
	suite.Require().Len(res, 1)
	suite.checkResItemAndCompare(batch[0], res[0])

	// Укладка нескольких элементов
	batch = batch[:0]
	batch = append(batch, BatchItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	}, BatchItem{
		CorrelationID: "b",
		Token:         someToken,
		OriginalURL:   someURL,
	})
	res, err = suite.storage.BatchStore(ctx, batch)
	suite.Require().NoError(err)
	suite.Require().Len(res, 2)
	suite.checkResItemAndCompare(batch[0], res[0])
	suite.checkResItemAndCompare(batch[1], res[1])
}

func (suite *MapBasedDBTestSuite) checkResItemAndCompare(reqIt BatchItem, resIt BatchItem) {
	suite.False(resIt.ConflictedToken)

	suite.False(resIt.ConflictedURL)
	suite.Empty(resIt.TokenOnConflictedURL)

	suite.Equal(reqIt.Token, resIt.Token)
	suite.Equal(reqIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(reqIt.CorrelationID, resIt.CorrelationID)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrTokenTaken() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewBatch(model.BatchShortenReq{})
	batch = append(batch, BatchItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	}, BatchItem{
		CorrelationID: "b",
		Token:         yandexToken, // Тут ожидается ErrTokenTaken
		OriginalURL:   someURL,
	})

	res, err := suite.storage.BatchStore(ctx, batch)

	suite.Require().Error(err)
	var errTT *ErrTokenTaken
	suite.ErrorAs(err, &errTT)

	suite.Require().Len(res, 2)

	// Беспроблемный элемент полностью валиден
	suite.checkResItemAndCompare(batch[0], res[0])

	// Проблемный элемент с ErrTokenTaken проверяется ниже
	resIt, reqIt := &res[1], &batch[1]

	// Поля и флаги Token/ErrTokenTaken адекванты ошибке
	suite.True(resIt.ConflictedToken)
	suite.Equal(yandexToken, errTT.Token)

	// Прочие поля / флаги проблемного элемента не выставлены в ошибки
	suite.False(resIt.ConflictedURL)
	suite.Empty(resIt.TokenOnConflictedURL)
	suite.Equal(reqIt.Token, resIt.Token)
	suite.Equal(reqIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(reqIt.CorrelationID, resIt.CorrelationID)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrOriginalURLExists() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewBatch(model.BatchShortenReq{})
	batch = append(batch, BatchItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   yandexURL, // Тут ожидается ErrOriginalURLExists
	}, BatchItem{
		CorrelationID: "b",
		Token:         someToken,
		OriginalURL:   someURL,
	})

	res, err := suite.storage.BatchStore(ctx, batch)

	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)

	suite.Require().Len(res, 2)

	// Беспроблемный элемент полностью валиден
	suite.checkResItemAndCompare(batch[1], res[1])

	// Проблемный элемент с ErrOriginalURLExists проверяется ниже
	resIt, reqIt := &res[0], &batch[0]

	// Поля / флаги ошибки ErrOriginalURLExists выставлены корректно
	suite.True(resIt.ConflictedURL)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexToken, resIt.TokenOnConflictedURL)

	// Прочие поля / флаги проблемного элемента не выставлены в ошибки
	suite.False(resIt.ConflictedToken)
	suite.Equal(reqIt.Token, resIt.Token)
	suite.Equal(reqIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(reqIt.CorrelationID, resIt.CorrelationID)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrOriginalURLExists_While_Token_Taken_Either() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewBatch(model.BatchShortenReq{})
	batch = append(batch, BatchItem{
		CorrelationID: "a",
		Token:         yandexToken, // Тут мог бы быть ErrTokenTaken, но при успехе его не должно быть
		OriginalURL:   yandexURL,   // Тут должен быть ErrOriginalURLExists
	})

	res, err := suite.storage.BatchStore(ctx, batch)

	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)

	var ett *ErrTokenTaken
	suite.NotErrorAs(err, &ett)

	suite.Require().Len(res, 1)
	resIt, batchIt := res[0], batch[0]

	// Поля / флаги ошибки ErrOriginalURLExists выставлены корректно
	suite.True(resIt.ConflictedURL)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexToken, resIt.TokenOnConflictedURL)

	// Прочие поля / флаги проблемного элемента не выставлены в ошибки
	// В частности, отсутствуют флаги-признаки ErrTokenTaken
	suite.False(resIt.ConflictedToken)
	suite.Equal(batchIt.Token, resIt.Token)
	suite.Equal(batchIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(batchIt.CorrelationID, resIt.CorrelationID)
}

// DeleteByTokens

func (suite *MapBasedDBTestSuite) TestDeleteByTokens_One_Item_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	suite.NoError(suite.storage.DeleteByTokens(context.Background(), []string{yandexToken}))
}

func (suite *MapBasedDBTestSuite) TestDeleteByTokens_All_Items_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	// Добавим еще элемент в хранилище
	errStore := suite.storage.Store(ctx, mailToken, mailURL)
	suite.Require().NoError(errStore)

	suite.Require().Len(suite.storage.data, 2)

	// Зачистим его полностью
	suite.NoError(suite.storage.DeleteByTokens(context.Background(), []string{yandexToken, mailToken}))
	suite.Assert().Empty(suite.storage.data)
}

func (suite *MapBasedDBTestSuite) TestDeleteByTokens_Some_Items_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	// Подготовим хранилище, добавив еще две записи
	batch := NewBatch(model.BatchShortenReq{})
	batch = append(batch, BatchItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	}, BatchItem{
		CorrelationID: "b",
		Token:         someToken,
		OriginalURL:   someURL,
	})
	res, err := suite.storage.BatchStore(ctx, batch)
	suite.Require().NoError(err)
	suite.Require().Len(res, 2)
	suite.Require().Len(suite.storage.data, 3)

	// Теперь в хранилище три элемента, два из которых мы удалим
	suite.NoError(suite.storage.DeleteByTokens(context.Background(), []string{yandexToken, goToken}))
	suite.Require().Len(suite.storage.data, 1)

	// Проверяем дополнительно, что в хранилище остался именно тот элемент,
	// удаление которого не запрошено
	url, err := suite.storage.Resolve(ctx, someToken)
	suite.Require().NoError(err)
	suite.Assert().Equal(someURL, url)
}

func (suite *MapBasedDBTestSuite) TestDeleteByTokens_Errors() {
	// В кейсе TokenToURL, хранилища основанного на мапе,
	// ошибок в этом методе нет
}

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
	userID = "8222be97-0266-40d1-b069-54f29508de43"

	yandexToken = "sPv80uUs"
	yandexURL   = "https://yandex.ru"

	mailToken = "9aF7e72i"
	mailURL   = "https://mail.ru"

	goToken = "GOTOKEN"
	goURL   = "https://go.dev"

	someToken = "0000ZZZZ"
	someURL   = "https://some.url.ru"

	deletedToken = "s8y7adb3"
)

func (s *MapBasedDBTestSuite) SetupTest() {
	s.storage = NewLocalStorage()
	if err := s.storage.Store(context.Background(), yandexToken, yandexURL, userID); err != nil {
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

func (suite *MapBasedDBTestSuite) TestResolve_ErrTokenIsDeleted() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	// Подготовка
	if err := suite.storage.Store(context.Background(), deletedToken, isDeleted, userID); err != nil {
		suite.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}

	// Прогон
	url, err := suite.storage.Resolve(context.Background(), deletedToken)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)
	suite.Equal("", url)
}

// Store

func (suite *MapBasedDBTestSuite) TestStore_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	errStore := suite.storage.Store(ctx, mailToken, mailURL, userID)
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

	err := suite.storage.Store(context.Background(), yandexToken, goURL, userID)
	suite.Require().Error(err)
	var errTT *ErrTokenTaken
	suite.ErrorAs(err, &errTT)
	suite.Equal(yandexToken, errTT.Token)
}

func (suite *MapBasedDBTestSuite) TestStore_ErrOriginalURLExists() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	err := suite.storage.Store(context.Background(), someToken, yandexURL, userID)
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

	err := suite.storage.Store(context.Background(), yandexToken, yandexURL, userID)
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

	batch := NewStoreBatch(model.BatchShortenReq{})

	// Пустой батч
	res, err := suite.storage.BatchStore(ctx, batch, userID)
	suite.Require().NoError(err)
	suite.Assert().Empty(res)

	// Укладка единственного элемента
	batch = append(batch, StoreItem{
		CorrelationID: "1",
		Token:         mailToken,
		OriginalURL:   mailURL,
	})
	res, err = suite.storage.BatchStore(ctx, batch, userID)
	suite.Require().NoError(err)
	suite.Require().Len(res, 1)
	suite.checkResItemAndCompare(batch[0], res[0])

	// Укладка нескольких элементов
	batch = batch[:0]
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	}, StoreItem{
		CorrelationID: "b",
		Token:         someToken,
		OriginalURL:   someURL,
	})
	res, err = suite.storage.BatchStore(ctx, batch, userID)
	suite.Require().NoError(err)
	suite.Require().Len(res, 2)
	suite.checkResItemAndCompare(batch[0], res[0])
	suite.checkResItemAndCompare(batch[1], res[1])
}

func (suite *MapBasedDBTestSuite) checkResItemAndCompare(reqIt StoreItem, resIt StoreItem) {
	suite.False(resIt.ConflictedToken)

	suite.False(resIt.ConflictedURL)

	suite.Equal(reqIt.Token, resIt.Token)
	suite.Equal(reqIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(reqIt.CorrelationID, resIt.CorrelationID)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrTokenTaken() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewStoreBatch(model.BatchShortenReq{})
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	}, StoreItem{
		CorrelationID: "b",
		Token:         yandexToken, // Тут ожидается ErrTokenTaken
		OriginalURL:   someURL,
	})

	res, err := suite.storage.BatchStore(ctx, batch, userID)

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
	suite.Equal(reqIt.Token, resIt.Token)
	suite.Equal(reqIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(reqIt.CorrelationID, resIt.CorrelationID)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrOriginalURLExists() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewStoreBatch(model.BatchShortenReq{})
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   yandexURL, // Тут ожидается ErrOriginalURLExists
	}, StoreItem{
		CorrelationID: "b",
		Token:         someToken,
		OriginalURL:   someURL,
	})

	res, err := suite.storage.BatchStore(ctx, batch, userID)

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
	suite.Equal(yandexToken, resIt.Token)

	// Прочие поля / флаги проблемного элемента не выставлены в ошибки
	suite.False(resIt.ConflictedToken)
	suite.Equal(reqIt.OriginalURL, resIt.OriginalURL)
	suite.Equal(reqIt.CorrelationID, resIt.CorrelationID)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrOriginalURLExists_While_Token_Taken_Either() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	batch := NewStoreBatch(model.BatchShortenReq{})
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         yandexToken, // Тут мог бы быть ErrTokenTaken, но при успехе его не должно быть
		OriginalURL:   yandexURL,   // Тут должен быть ErrOriginalURLExists
	})

	res, err := suite.storage.BatchStore(ctx, batch, userID)

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
	suite.Equal(yandexToken, resIt.Token)

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
	errStore := suite.storage.Store(ctx, mailToken, mailURL, userID)
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
	batch := NewStoreBatch(model.BatchShortenReq{})
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	}, StoreItem{
		CorrelationID: "b",
		Token:         someToken,
		OriginalURL:   someURL,
	})
	res, err := suite.storage.BatchStore(ctx, batch, userID)
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

// CheckUserExists

func (s *MapBasedDBTestSuite) TestCheckUserExists_UserNotExists() {
	exists, err := s.storage.CheckUserExists(context.Background(), "non-existent-user")
	s.Require().NoError(err)
	s.False(exists)
}

func (s *MapBasedDBTestSuite) TestCheckUserExists_UserExists() {
	// Создаем пользователя
	uid, err := s.storage.CreateUser(context.Background())
	s.Require().NoError(err)

	exists, err := s.storage.CheckUserExists(context.Background(), uid)
	s.Require().NoError(err)
	s.True(exists)
}

// CreateUser

func (s *MapBasedDBTestSuite) TestCreateUser() {
	uid, err := s.storage.CreateUser(context.Background())
	s.Require().NoError(err)
	s.NotEmpty(uid)

	// Проверяем, что пользователь создался
	exists, err := s.storage.CheckUserExists(context.Background(), uid)
	s.Require().NoError(err)
	s.True(exists)
}

// ListUserURLs

func (s *MapBasedDBTestSuite) TestListUserURLs_EmptyUser() {
	// Создаем нового пользователя без URL
	uid, err := s.storage.CreateUser(context.Background())
	s.Require().NoError(err)

	urls, err := s.storage.ListUserURLs(context.Background(), uid)
	s.Require().NoError(err)
	s.Empty(urls)
}

func (s *MapBasedDBTestSuite) TestListUserURLs_ExistingUser() {
	// Проверяем, что тестовые данные из SetupTest() корректно возвращаются
	urls, err := s.storage.ListUserURLs(context.Background(), userID)
	s.Require().NoError(err)
	s.Len(urls, 1)

	s.Equal(yandexToken, urls[0].ShortURL)
	s.Equal(yandexURL, urls[0].OriginalURL)
}

func (s *MapBasedDBTestSuite) TestListUserURLs_NonExistentUser() {
	urls, err := s.storage.ListUserURLs(context.Background(), "non-existent-user")
	s.Require().NoError(err)
	s.Empty(urls)
}

func (s *MapBasedDBTestSuite) TestListUserURLs_MultipleURLs() {
	// Добавляем несколько URL для пользователя
	if err := s.storage.Store(context.Background(), mailToken, mailURL, userID); err != nil {
		s.Fail("Ошибка при сохранении mailURL")
	}
	if err := s.storage.Store(context.Background(), goToken, goURL, userID); err != nil {
		s.Fail("Ошибка при сохранении goURL")
	}

	urls, err := s.storage.ListUserURLs(context.Background(), userID)
	s.Require().NoError(err)
	s.Len(urls, 3) // Yandex + Mail + Go

	// Проверяем наличие всех URL
	foundYandex := false
	foundMail := false
	foundGo := false

	for _, url := range urls {
		switch url.ShortURL {
		case yandexToken:
			foundYandex = true
		case mailToken:
			foundMail = true
		case goToken:
			foundGo = true
		}
	}

	s.True(foundYandex)
	s.True(foundMail)
	s.True(foundGo)
}

// MarkUserURLsDeleted

func (s *MapBasedDBTestSuite) TestMarkUserURLsDeleted() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// токен yandexToken принадлежит пользователю userID;
	// других данных в нём нет

	batch := make(ToMarkDeletedReqBatch, 0, 1)
	batch = append(batch, ToMarkDeletedReqItem{
		Token:  yandexToken,
		UserID: userID,
	})

	// Удаление отрабатывает без ошибок
	s.Require().NoError(s.storage.MarkUserURLsDeleted(context.Background(), batch))

	// Resolve ранее удаленного токена возвращает пустой URL и ошибку ErrTokenIsDeleted
	url, err := s.storage.Resolve(context.Background(), yandexToken)
	s.Assert().Empty(url)
	var errTID *ErrTokenIsDeleted
	s.ErrorAs(err, &errTID)

	// Повторное удаление походит без ошибок
	s.Require().NoError(s.storage.MarkUserURLsDeleted(context.Background(), batch))
	// И не меняет данных в хранилище
	url, err = s.storage.Resolve(context.Background(), yandexToken)
	s.Assert().Empty(url)
	s.ErrorAs(err, &errTID)
}

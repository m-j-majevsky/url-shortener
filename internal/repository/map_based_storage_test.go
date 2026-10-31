package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

func (suite *MapBasedDBTestSuite) SetupTest() {
	suite.storage = NewLocalStorage()
	if err := suite.storage.Store(context.Background(), yandexToken, yandexURL, userID); err != nil {
		suite.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

func TestMapBasedDBTestSuite(t *testing.T) {
	suite.Run(t, new(MapBasedDBTestSuite))
}

// markYandexDeleted подготавливает мягкое удаление токена yandexToken
// (владелец — userID): семантика части 7, где удалённая запись сохраняется
// в хранилище с признаком удаления.
func (suite *MapBasedDBTestSuite) markYandexDeleted() {
	batch := make(ToMarkDeletedReqBatch, 0, 1)
	batch = append(batch, ToMarkDeletedReqItem{
		Token:  yandexToken,
		UserID: userID,
	})
	if err := suite.storage.MarkUserURLsDeleted(context.Background(), batch); err != nil {
		suite.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
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

	// Подготовка: храним отдельный токен и мягко удаляем его
	// (в старой семантике "удаление" делалось особым вызовом Store — сейчас
	// удаление всегда через MarkUserURLsDeleted, часть 7)
	if err := suite.storage.Store(context.Background(), deletedToken, mailURL, userID); err != nil {
		suite.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
	batch := make(ToMarkDeletedReqBatch, 0, 1)
	batch = append(batch, ToMarkDeletedReqItem{Token: deletedToken, UserID: userID})
	suite.Require().NoError(suite.storage.MarkUserURLsDeleted(context.Background(), batch))

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

	url, errResolve := suite.storage.Resolve(ctx, mailToken)
	suite.Require().NoError(errResolve)
	suite.Equal(mailURL, url)

	url, errResolve = suite.storage.Resolve(ctx, yandexToken)
	suite.Require().NoError(errResolve)
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

// Семантика части 7: пересохранение удалённого URL

func (suite *MapBasedDBTestSuite) TestStore_ErrOriginalURLExists_After_Deletion() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	// Подготовка: мягкое удаление
	suite.markYandexDeleted()

	// Пересохранение удалённого URL даёт конфликт со старым токеном,
	// как в PG (ON CONFLICT срабатывает и на строке с is_deleted)
	err := suite.storage.Store(ctx, someToken, yandexURL, userID)
	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexURL, eoue.URL)

	// Данные не перезаписаны: новый токен не сохранился
	url, err := suite.storage.Resolve(ctx, someToken)
	suite.Empty(url)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)

	// Старый токен по-прежнему отвечает 410
	url, err = suite.storage.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)
}

func (suite *MapBasedDBTestSuite) TestStore_ErrTokenTaken_After_Deletion() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()
	suite.markYandexDeleted()

	// Удалённый токен остаётся занятым: ни успеха, ни 410
	err := suite.storage.Store(ctx, yandexToken, goURL, userID)
	suite.Require().Error(err)
	var errTT *ErrTokenTaken
	suite.ErrorAs(err, &errTT)
	suite.Equal(yandexToken, errTT.Token)

	// Запись не перезаписана, признак удаления не снят
	url, err := suite.storage.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)
}

// BatchStore

func (suite *MapBasedDBTestSuite) TestBatchStore_Success() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()

	// Пустой батч
	batch := make(StoreBatch, 0)
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

	batch := make(StoreBatch, 0, 2)
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

	// Поля и флаги Token/ErrTokenTaken адекватны ошибке
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

	batch := make(StoreBatch, 0, 2)
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

	batch := make(StoreBatch, 0, 1)
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

func (suite *MapBasedDBTestSuite) TestBatchStore_ErrOriginalURLExists_After_Deletion() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	ctx := context.Background()
	suite.markYandexDeleted()

	// Через батч пересохранение удалённого URL тоже даёт конфликт со старым токеном
	batch := make(StoreBatch, 0, 1)
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         someToken,
		OriginalURL:   yandexURL,
	})

	res, err := suite.storage.BatchStore(ctx, batch, userID)

	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(yandexToken, eoue.StoredToken)

	suite.Require().Len(res, 1)
	suite.True(res[0].ConflictedURL)
	suite.False(res[0].ConflictedToken)
	// Токен в ответе заменён на старый
	suite.Equal(yandexToken, res[0].Token)
	suite.Equal(yandexURL, res[0].OriginalURL)
	suite.Equal("a", res[0].CorrelationID)

	// Данные не перезаписаны
	url, err := suite.storage.Resolve(ctx, someToken)
	suite.Empty(url)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_MixedScenario() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет
	// Смешанный батч: чистая запись + конфликт по URL + конфликт по токену;
	// зеркалит MixedScenario из pgxmock-тестов

	ctx := context.Background()

	batch := make(StoreBatch, 0, 3)
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL, // чистая запись
	}, StoreItem{
		CorrelationID: "b",
		Token:         mailToken,
		OriginalURL:   yandexURL, // конфликт по URL
	}, StoreItem{
		CorrelationID: "c",
		Token:         yandexToken,
		OriginalURL:   someURL, // конфликт по токену
	})

	res, err := suite.storage.BatchStore(ctx, batch, userID)

	suite.Require().Error(err)

	// Ошибка — joined: в ней есть и ErrOriginalURLExists, и ErrTokenTaken
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexURL, eoue.URL)

	var errTT *ErrTokenTaken
	suite.ErrorAs(err, &errTT)
	suite.Equal(yandexToken, errTT.Token)

	suite.Require().Len(res, 3)

	// Чистый элемент — валиден
	suite.False(res[0].ConflictedURL)
	suite.False(res[0].ConflictedToken)
	suite.Equal(goToken, res[0].Token)
	suite.Equal(goURL, res[0].OriginalURL)

	// Конфликт по URL: токен заменён на старый, флагов токена нет
	suite.True(res[1].ConflictedURL)
	suite.False(res[1].ConflictedToken)
	suite.Equal(yandexToken, res[1].Token)
	suite.Equal(yandexURL, res[1].OriginalURL)
	suite.Equal("b", res[1].CorrelationID)

	// Конфликт по токену: данные исходные, флага URL нет
	suite.True(res[2].ConflictedToken)
	suite.False(res[2].ConflictedURL)
	suite.Equal(yandexToken, res[2].Token)
	suite.Equal(someURL, res[2].OriginalURL)
	suite.Equal("c", res[2].CorrelationID)
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
	batch := make(StoreBatch, 0, 2)
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

func (suite *MapBasedDBTestSuite) TestDeleteByTokens_ClearsDeletedMark() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет
	// Жёсткое удаление снимает признак мягкого: после него токен
	// отвечает 404, а не 410 — как в PG, где строка снесена целиком

	ctx := context.Background()
	suite.markYandexDeleted()

	// До жёсткого удаления — 410
	url, err := suite.storage.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)

	// Жёсткое удаление
	suite.NoError(suite.storage.DeleteByTokens(ctx, []string{yandexToken}))

	// После — 404, а не 410
	url, err = suite.storage.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)
}

// CheckUserExists

func (suite *MapBasedDBTestSuite) TestCheckUserExists_UserNotExists() {
	exists, err := suite.storage.CheckUserExists(context.Background(), "non-existent-user")
	suite.Require().NoError(err)
	suite.False(exists)
}

func (suite *MapBasedDBTestSuite) TestCheckUserExists_UserExists() {
	// Создаем пользователя
	uid, err := suite.storage.CreateUser(context.Background())
	suite.Require().NoError(err)

	exists, err := suite.storage.CheckUserExists(context.Background(), uid)
	suite.Require().NoError(err)
	suite.True(exists)
}

// CreateUser

func (suite *MapBasedDBTestSuite) TestCreateUser() {
	uid, err := suite.storage.CreateUser(context.Background())
	suite.Require().NoError(err)
	suite.NotEmpty(uid)

	// Проверяем, что пользователь создался
	exists, err := suite.storage.CheckUserExists(context.Background(), uid)
	suite.Require().NoError(err)
	suite.True(exists)
}

// ListUserURLs

func (suite *MapBasedDBTestSuite) TestListUserURLs_EmptyUser() {
	// Создаем нового пользователя без URL
	uid, err := suite.storage.CreateUser(context.Background())
	suite.Require().NoError(err)

	urls, err := suite.storage.ListUserURLs(context.Background(), uid)
	suite.Require().NoError(err)
	suite.Empty(urls)
}

func (suite *MapBasedDBTestSuite) TestListUserURLs_ExistingUser() {
	// Проверяем, что тестовые данные из SetupTest() корректно возвращаются
	urls, err := suite.storage.ListUserURLs(context.Background(), userID)
	suite.Require().NoError(err)
	suite.Len(urls, 1)

	suite.Equal(yandexToken, urls[0].ShortURL)
	suite.Equal(yandexURL, urls[0].OriginalURL)
}

func (suite *MapBasedDBTestSuite) TestListUserURLs_NonExistentUser() {
	urls, err := suite.storage.ListUserURLs(context.Background(), "non-existent-user")
	suite.Require().NoError(err)
	suite.Empty(urls)
}

func (suite *MapBasedDBTestSuite) TestListUserURLs_MultipleURLs() {
	// Добавляем несколько URL для пользователя
	if err := suite.storage.Store(context.Background(), mailToken, mailURL, userID); err != nil {
		suite.Fail("Ошибка при сохранении mailURL")
	}
	if err := suite.storage.Store(context.Background(), goToken, goURL, userID); err != nil {
		suite.Fail("Ошибка при сохранении goURL")
	}

	urls, err := suite.storage.ListUserURLs(context.Background(), userID)
	suite.Require().NoError(err)
	suite.Len(urls, 3) // Yandex + Mail + Go

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

	suite.True(foundYandex)
	suite.True(foundMail)
	suite.True(foundGo)
}

func (suite *MapBasedDBTestSuite) TestListUserURLs_ExcludesDeleted() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет
	// Удалённые записи не попадают в листинг, но живут в хранилище

	ctx := context.Background()

	// Добавляем живую запись
	suite.Require().NoError(suite.storage.Store(ctx, mailToken, mailURL, userID))

	// Мягко удаляем первую
	suite.markYandexDeleted()

	// В листинге — только живая запись
	urls, err := suite.storage.ListUserURLs(ctx, userID)
	suite.Require().NoError(err)
	suite.Require().Len(urls, 1)
	suite.Equal(mailToken, urls[0].ShortURL)
	suite.Equal(mailURL, urls[0].OriginalURL)

	// Удалённая запись при этом живёт в хранилище: Resolve даёт 410
	url, err := suite.storage.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)
}

// MarkUserURLsDeleted

func (suite *MapBasedDBTestSuite) TestMarkUserURLsDeleted() {
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
	suite.Require().NoError(suite.storage.MarkUserURLsDeleted(context.Background(), batch))

	// Resolve ранее удаленного токена возвращает пустой URL и ошибку ErrTokenIsDeleted
	url, err := suite.storage.Resolve(context.Background(), yandexToken)
	suite.Assert().Empty(url)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)

	// Повторное удаление походит без ошибок
	suite.Require().NoError(suite.storage.MarkUserURLsDeleted(context.Background(), batch))
	// И не меняет данных в хранилище
	url, err = suite.storage.Resolve(context.Background(), yandexToken)
	suite.Assert().Empty(url)
	suite.ErrorAs(err, &errTID)
}

func (suite *MapBasedDBTestSuite) TestMarkUserURLsDeleted_TokenNotOwned() {
	// Контекст:
	// (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет
	// Чужие токены не затрагиваются, несуществующие игнорируются без ошибок —
	// как в PG, где запрос ограничен WHERE token AND user_id

	ctx := context.Background()

	// Чужой пользователь
	uid2, err := suite.storage.CreateUser(ctx)
	suite.Require().NoError(err)

	batch := make(ToMarkDeletedReqBatch, 0, 2)
	batch = append(batch,
		ToMarkDeletedReqItem{Token: yandexToken, UserID: uid2}, // токен чужой — не затрагивается
		ToMarkDeletedReqItem{Token: someToken, UserID: userID}, // токена нет в хранилище — игнорируется
	)

	// Ошибок нет
	suite.Require().NoError(suite.storage.MarkUserURLsDeleted(ctx, batch))

	// Чужой токен не помечен удалённым: resolve работает
	url, err := suite.storage.Resolve(ctx, yandexToken)
	suite.Require().NoError(err)
	suite.Equal(yandexURL, url)
}

// Ping

func (suite *MapBasedDBTestSuite) TestPing() {
	// Хранилище целиком в памяти и всегда доступно
	suite.NoError(suite.storage.Ping(context.Background()))
}

// SaveToFile / LoadFromFile

func (suite *MapBasedDBTestSuite) TestSaveLoad_RoundTrip() {
	// Полный круг: сохранить состояние в файл, загрузить в новое хранилище.
	// Признак удаления и привязка к пользователям переживают круг

	ctx := context.Background()

	// Второй пользователь со своей записью
	uid2, err := suite.storage.CreateUser(ctx)
	suite.Require().NoError(err)
	suite.Require().NoError(suite.storage.Store(ctx, mailToken, mailURL, uid2))

	// Мягко удаляем запись первого пользователя
	suite.markYandexDeleted()

	path := filepath.Join(suite.T().TempDir(), "state.json")
	suite.Require().NoError(suite.storage.SaveToFile(path))

	// Новое хранилище, состояние загружается из файла
	loaded := NewLocalStorage()
	suite.Require().NoError(loaded.LoadFromFile(path))

	// Удалённая запись восстановилась с признаком удаления — 410, а не 404
	url, err := loaded.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTID *ErrTokenIsDeleted
	suite.ErrorAs(err, &errTID)

	// Живая запись восстановилась полностью
	url, err = loaded.Resolve(ctx, mailToken)
	suite.Require().NoError(err)
	suite.Equal(mailURL, url)

	// Привязка записей к пользователям сохранилась
	urls, err := loaded.ListUserURLs(ctx, uid2)
	suite.Require().NoError(err)
	suite.Require().Len(urls, 1)
	suite.Equal(mailToken, urls[0].ShortURL)
	suite.Equal(mailURL, urls[0].OriginalURL)

	exists, err := loaded.CheckUserExists(ctx, uid2)
	suite.Require().NoError(err)
	suite.True(exists)
}

func (suite *MapBasedDBTestSuite) TestLoadFromFile_OverwritesNotMerges() {
	// Загрузка перезаписывает состояние целиком, а не мёржит

	ctx := context.Background()

	path := filepath.Join(suite.T().TempDir(), "state.json")
	suite.Require().NoError(suite.storage.SaveToFile(path))
	// В файле одна запись: yandexToken -> yandexURL

	// Хранилище с посторонними данными
	other := NewLocalStorage()
	suite.Require().NoError(other.Store(ctx, goToken, goURL, userID))

	// Загрузка
	suite.Require().NoError(other.LoadFromFile(path))

	// Посторонней записи больше нет
	url, err := other.Resolve(ctx, goToken)
	suite.Empty(url)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)

	// Загруженная запись на месте
	url, err = other.Resolve(ctx, yandexToken)
	suite.Require().NoError(err)
	suite.Equal(yandexURL, url)
}

func (suite *MapBasedDBTestSuite) TestLoadFromFile_ErrNoFile() {
	// Ошибка отсутствия файла; состояние хранилища не меняется

	path := filepath.Join(suite.T().TempDir(), "no_such_state.json")

	err := suite.storage.LoadFromFile(path)
	suite.Require().Error(err)
	suite.Contains(err.Error(), "ошибка чтения файла состояния хранилища")

	// Состояние не изменилось
	url, err := suite.storage.Resolve(context.Background(), yandexToken)
	suite.Require().NoError(err)
	suite.Equal(yandexURL, url)
}

func (suite *MapBasedDBTestSuite) TestLoadFromFile_ErrInvalidJSON() {
	// Ошибка невалидного JSON; состояние хранилища не меняется

	path := filepath.Join(suite.T().TempDir(), "state.json")
	suite.Require().NoError(os.WriteFile(path, []byte("{oops"), 0644))

	err := suite.storage.LoadFromFile(path)
	suite.Require().Error(err)
	suite.Contains(err.Error(), "ошибка декодирования содержимого файла состояния хранилища")

	url, err := suite.storage.Resolve(context.Background(), yandexToken)
	suite.Require().NoError(err)
	suite.Equal(yandexURL, url)
}

// Регрессионные кейсы индекса original_url -> token.
// Инвариант: для каждой записи data[token]=url существует byURL[url]=token
// и наоборот; отображение биективно, так как Store/BatchStore не записывают
// дубликаты URL. Жёсткое удаление обязано синхронно чистить обе мапы,
// загрузка из файла — восстанавливать индекс.

func (suite *MapBasedDBTestSuite) checkIndexInvariantOn(s *LocalStorage) {
	suite.Require().Equal(len(s.data), len(s.byURL),
		"размеры data и byURL разошлись")
	for tok, url := range s.data {
		got, ok := s.byURL[url]
		suite.Require().True(ok, "в индексе нет URL записи %s", tok)
		suite.Require().Equal(tok, got, "индекс указывает не на свой токен")
	}
}

func (suite *MapBasedDBTestSuite) TestStore_Success_After_Hard_Deletion() {
	// Ключевой регрессионный кейс индекса: забытый delete(s.byURL, url)
	// превратил бы повторное сохранение удалённого URL в фантомный конфликт

	ctx := context.Background()

	// Подготовка: жёсткое удаление
	suite.Require().NoError(suite.storage.DeleteByTokens(ctx, []string{yandexToken}))

	// Повторное сохранение того же URL — успех с новым токеном
	suite.Require().NoError(suite.storage.Store(ctx, someToken, yandexURL, userID))

	url, err := suite.storage.Resolve(ctx, someToken)
	suite.Require().NoError(err)
	suite.Equal(yandexURL, url)

	// Старый токен отвечает 404, а не 410 и не отдаёт URL
	url, err = suite.storage.Resolve(ctx, yandexToken)
	suite.Empty(url)
	var errTNF *ErrTokenNotFound
	suite.ErrorAs(err, &errTNF)

	suite.checkIndexInvariantOn(suite.storage)
}

func (suite *MapBasedDBTestSuite) TestBatchStore_DuplicateURLInsideBatch() {
	// Повтор URL внутри одного пакета: первый элемент записывается
	// и пополняет индекс, второй ловит конфликт с токеном первого

	ctx := context.Background()

	batch := make(StoreBatch, 0, 3)
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL, // чистая запись
	}, StoreItem{
		CorrelationID: "b",
		Token:         mailToken,
		OriginalURL:   goURL, // повтор URL внутри пакета
	}, StoreItem{
		CorrelationID: "c",
		Token:         someToken,
		OriginalURL:   someURL, // чистая запись
	})

	res, err := suite.storage.BatchStore(ctx, batch, userID)

	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(goToken, eoue.StoredToken)
	suite.Equal(goURL, eoue.URL)

	suite.Require().Len(res, 3)

	// Первый элемент записан как чистый
	suite.False(res[0].ConflictedURL)
	suite.False(res[0].ConflictedToken)
	suite.Equal(goToken, res[0].Token)

	// Второй ловит конфликт по URL с токеном первого
	suite.True(res[1].ConflictedURL)
	suite.False(res[1].ConflictedToken)
	suite.Equal(goToken, res[1].Token)
	suite.Equal(goURL, res[1].OriginalURL)
	suite.Equal("b", res[1].CorrelationID)

	// Третий чистый
	suite.False(res[2].ConflictedURL)
	suite.False(res[2].ConflictedToken)

	// URL записан ровно один раз, инвариант цел
	url, err := suite.storage.Resolve(ctx, goToken)
	suite.Require().NoError(err)
	suite.Equal(goURL, url)
	suite.checkIndexInvariantOn(suite.storage)
}

func (suite *MapBasedDBTestSuite) TestSaveLoad_RoundTrip_IndexRestored() {
	// Индекс восстанавливается при загрузке из файла; без этого
	// проверка уникальности деградировала бы до «всё новое»

	ctx := context.Background()

	path := filepath.Join(suite.T().TempDir(), "state.json")
	suite.Require().NoError(suite.storage.SaveToFile(path))

	loaded := NewLocalStorage()
	suite.Require().NoError(loaded.LoadFromFile(path))

	// Дубликат уже сохранённого URL ловится после загрузки
	err := loaded.Store(ctx, someToken, yandexURL, userID)
	suite.Require().Error(err)
	var eoue *ErrOriginalURLExists
	suite.ErrorAs(err, &eoue)
	suite.Equal(yandexToken, eoue.StoredToken)
	suite.Equal(yandexURL, eoue.URL)

	// Новый URL сохраняется и ловится повторно
	suite.Require().NoError(loaded.Store(ctx, someToken, someURL, userID))
	err = loaded.Store(ctx, mailToken, someURL, userID)
	suite.Require().Error(err)
	suite.ErrorAs(err, &eoue)
	suite.Equal(someToken, eoue.StoredToken)

	// Инвариант на загруженном хранилище
	suite.checkIndexInvariantOn(loaded)
}

func (suite *MapBasedDBTestSuite) TestIndexInvariant_AfterMixedOperations() {
	// Инвариант под серией разнородных операций

	ctx := context.Background()

	// Store
	suite.Require().NoError(suite.storage.Store(ctx, mailToken, mailURL, userID))

	// BatchStore
	batch := make(StoreBatch, 0, 1)
	batch = append(batch, StoreItem{
		CorrelationID: "a",
		Token:         goToken,
		OriginalURL:   goURL,
	})
	_, err := suite.storage.BatchStore(ctx, batch, userID)
	suite.Require().NoError(err)
	suite.checkIndexInvariantOn(suite.storage)

	// Мягкое удаление: записи в data и индексе не меняются —
	// URL остаётся «занятым», повторный POST даёт конфликт со старым токеном
	suite.markYandexDeleted()
	suite.checkIndexInvariantOn(suite.storage)

	// Жёсткое удаление: записи уходят из обеих мап синхронно
	suite.Require().NoError(suite.storage.DeleteByTokens(ctx, []string{goToken, mailToken}))
	suite.checkIndexInvariantOn(suite.storage)
}

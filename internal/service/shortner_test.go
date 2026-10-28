package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service/mocks"
)

// Компиляторные проверки: и реальное хранилище, и mockery-мок
// удовлетворяют композитному интерфейсу сервиса.
var (
	_ Storage = (*repository.LocalStorage)(nil)
	_ Storage = (*mocks.MockStorage)(nil)
)

type ShortenerSuite struct {
	suite.Suite
	storage *repository.LocalStorage
	svc     *Shortener
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
	someURL   = "http://some.url.ru"
)

func (s *ShortenerSuite) SetupTest() {
	s.storage = repository.NewLocalStorage()
	if err := s.storage.Store(s.T().Context(), yandexToken, yandexURL, userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

func TestShortenerSuite(t *testing.T) {
	suite.Run(t, new(ShortenerSuite))
}

// Service Configuration tests

func createShortenerTestConfig(storage Storage) ShortenerConfig {
	cfg := DefaultShortenerConfig()
	cfg.Storage = storage
	return cfg
}

func (s *ShortenerSuite) createShortenerInstance(storage Storage) {
	cfg := createShortenerTestConfig(storage)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)
	s.Require().NotNil(svc)
	s.svc = svc
}

// createMockStorage создаёт mockery-мок композитного интерфейса Storage,
// привязанный к текущему тесту: незапланированный вызов любого метода
// хранилища уронит тест паникой (нет заготовленного ответа).
func (s *ShortenerSuite) createMockStorage() *mocks.MockStorage {
	return mocks.NewMockStorage(s.T())
}

func (s *ShortenerSuite) TestNewShortener_ValidConfig_CreatesInstance() {
	// Проверка заодно удостоверяет, что mockery-мок удовлетворяет Storage
	s.createShortenerInstance(s.storage)
}

func (s *ShortenerSuite) TestNewShortener_InvalidConfig_ReturnsError() {
	cases := []struct {
		name string
		fix  func(cfg *ShortenerConfig)
	}{
		{"нет хранилища", func(c *ShortenerConfig) { c.Storage = nil }},
		{"нет генератора случайных данных", func(c *ShortenerConfig) { c.RandProv = nil }},
		{"неположительное число генерируемых байт", func(c *ShortenerConfig) { c.BytesToGenerate = 0 }},
		{"неположительная минимальная длина токена", func(c *ShortenerConfig) { c.MinTokenLength = 0 }},
		{"наименьшая длина больше наибольшей", func(c *ShortenerConfig) {
			c.MinTokenLength = 10
			c.MaxTokenLength = 5
		}},
		{"неположительное число попыток генерации", func(c *ShortenerConfig) { c.MaxGeneratingAttempts = 0 }},
		{"неположительное число попыток сохранения", func(c *ShortenerConfig) { c.MaxStoringAttempts = 0 }},
		{"неположительный размер батча удалений", func(c *ShortenerConfig) { c.DeletionBatchSize = 0 }},
		{"неположительная глубина очереди удалений", func(c *ShortenerConfig) { c.DeletionQueueBuffer = 0 }},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			cfg := createShortenerTestConfig(s.storage)
			tc.fix(&cfg)

			_, err := NewShortener(cfg)
			s.Error(err)
			s.Contains(err.Error(), errCfgHeader)
		})
	}
}

// generateTokens

func (s *ShortenerSuite) TestGenerateTokens_Size_Fits() {
	s.createShortenerInstance(s.storage)

	for i := 0; i <= 1000; i += 50 {
		tokens, err := s.svc.generateTokens(i)
		s.NoError(err)
		s.Equal(i, len(tokens))
	}
}

func (s *ShortenerSuite) TestGenerateTokens_LengthInRange_And_Distinct() {
	s.createShortenerInstance(s.storage)

	for i := 0; i < 100; i++ {
		tokens, err := s.svc.generateTokens(5)
		s.Require().NoError(err)
		s.Require().Equal(5, len(tokens))

		uniq := make(map[string]struct{}, 5)
		for _, tok := range tokens {
			uniq[tok] = struct{}{}
			s.True(len(tok) >= s.svc.config.MinTokenLength && len(tok) <= s.svc.config.MaxTokenLength)
			s.NoError(encoding.IsValidBase62(tok))
		}
		// Все токены различны — по контракту generateTokens
		s.Equal(5, len(uniq))
	}
}

type FixedReader struct {
	Data []byte
	Pos  int
}

func (r *FixedReader) Read(p []byte) (int, error) {
	if r.Pos >= len(r.Data) {
		return 0, io.EOF
	}
	n := copy(p, r.Data[r.Pos:])
	r.Pos += n
	return n, nil
}

func (s *ShortenerSuite) TestGenerateTokens_RetryOnLengthMismatch_WithFixedProvider() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.MinTokenLength = 8
	cfg.MaxTokenLength = 10
	cfg.BytesToGenerate = 6
	cfg.MaxGeneratingAttempts = 3

	badBytes := make([]byte, cfg.BytesToGenerate) // все нули
	data := make([]byte, 0, cfg.BytesToGenerate*cfg.MaxGeneratingAttempts)
	for i := 0; i < cfg.MaxGeneratingAttempts; i++ {
		data = append(data, badBytes...)
	}

	cfg.RandProv = &FixedReader{Data: data}

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	_, err = svc.generateTokens(1)
	s.Error(err)
	s.Contains(err.Error(), fmt.Sprintf("за %d попыток", cfg.MaxGeneratingAttempts))
}

type FailingReader struct{}

var ErrFailingReader = errors.New("ошибка провайдера")

func (FailingReader) Read([]byte) (int, error) {
	return 0, ErrFailingReader
}

func (s *ShortenerSuite) TestGenerateRandomBytes_ErrorWhenProviderFails() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.RandProv = FailingReader{}
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	_, err = svc.generateTokens(1)
	s.Error(err)
	s.Contains(err.Error(), "ошибка генерации случайного токена")
	s.ErrorIs(err, ErrFailingReader)
}

// Resolve

func (s *ShortenerSuite) TestResolve_ExistingToken() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	url, err := s.svc.Resolve(s.T().Context(), yandexToken)
	s.Require().NoError(err)
	s.Equal(yandexURL, url)
}

func (s *ShortenerSuite) TestResolve_ErrTokenNotFound() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	url, err := s.svc.Resolve(s.T().Context(), someToken)
	var errTNF *ErrTokenNotFound
	s.ErrorAs(err, &errTNF)
	s.Equal(someToken, errTNF.Token)
	s.Equal("", url)
}

func (s *ShortenerSuite) TestResolve_ErrTokenIsDeleted() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	// Подготовка: мягкое удаление через хранилище напрямую
	toMarkDeleted := repository.ToMarkDeletedReqBatch{
		repository.ToMarkDeletedReqItem{Token: yandexToken, UserID: userID},
	}
	if err := s.storage.MarkUserURLsDeleted(s.T().Context(), toMarkDeleted); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}

	s.createShortenerInstance(s.storage)

	url, err := s.svc.Resolve(s.T().Context(), yandexToken)
	var errTID *ErrTokenIsDeleted
	s.ErrorAs(err, &errTID)
	s.Equal(yandexToken, errTID.Token)
	s.Equal("", url)
}

func (s *ShortenerSuite) TestResolve_Mock_TranslatesErrTokenIsDeleted() {
	m := s.createMockStorage()
	m.EXPECT().Resolve(mock.Anything, yandexToken).Return("", repository.NewErrTokenIsDeleted(yandexToken)).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	url, err := svc.Resolve(s.T().Context(), yandexToken)
	var errTID *ErrTokenIsDeleted
	s.ErrorAs(err, &errTID)
	s.Equal(yandexToken, errTID.Token)
	s.Equal("", url)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestResolve_Mock_WrapsOtherErrors() {
	m := s.createMockStorage()
	origErr := errors.New("БД недоступна")
	m.EXPECT().Resolve(mock.Anything, someToken).Return("", origErr).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	url, err := svc.Resolve(s.T().Context(), someToken)
	s.Error(err)
	s.Contains(err.Error(), "ошибка хранилища")
	s.ErrorIs(err, origErr)
	s.Equal("", url)
	m.AssertExpectations(s.T())
}

// GenerateAndStore

func (s *ShortenerSuite) TestGenerateAndStore_Success() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	token, err := s.svc.GenerateAndStore(s.T().Context(), mailURL, userID)
	s.Require().NoError(err)
	s.NotEmpty(token)
	s.NoError(encoding.IsValidBase62(token))

	// URL действительно сохранился
	url, err := s.svc.Resolve(s.T().Context(), token)
	s.Require().NoError(err)
	s.Equal(mailURL, url)
}

func (s *ShortenerSuite) TestGenerateAndStore_Got_ErrOriginalURLExists() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	token, err := s.svc.GenerateAndStore(s.T().Context(), yandexURL, userID)

	var eoue *ErrOriginalURLExists
	s.Require().ErrorAs(err, &eoue)
	s.Assert().Equal(yandexToken, eoue.StoredToken)
	s.Assert().Equal(yandexURL, eoue.URL)
	s.Assert().Equal(yandexToken, token)

	s.NoError(encoding.IsValidBase62(token))
}

func (s *ShortenerSuite) TestGenerateAndStore_Mock_RetriesOnTakenToken() {
	m := s.createMockStorage()

	var attemptTokens []string
	m.EXPECT().Store(mock.Anything, mock.Anything, mailURL, userID).RunAndReturn(
		func(ctx context.Context, token, url, uid string) error {
			attemptTokens = append(attemptTokens, token)
			if len(attemptTokens) == 1 {
				return repository.NewErrTokenTaken(token)
			}
			return nil
		}).Times(2)

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	token, err := svc.GenerateAndStore(s.T().Context(), mailURL, userID)
	s.Require().NoError(err)
	s.Require().Len(attemptTokens, 2)

	// Токены попыток различаются: конфликт урегулирован ретраем
	s.NotEqual(attemptTokens[0], attemptTokens[1])
	s.Equal(attemptTokens[1], token)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestGenerateAndStore_Mock_ExhaustedAttempts() {
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	cfg.MaxStoringAttempts = 3
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	m.EXPECT().Store(mock.Anything, mock.Anything, mailURL, userID).
		Return(repository.NewErrTokenTaken("some-taken-token")).Times(3)

	token, err := svc.GenerateAndStore(s.T().Context(), mailURL, userID)
	s.Error(err)
	s.Contains(err.Error(), fmt.Sprintf("за %d попыток", cfg.MaxStoringAttempts))
	s.Equal("", token)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestGenerateAndStore_Mock_WrapsOtherErrors() {
	m := s.createMockStorage()
	origErr := errors.New("БД недоступна")
	m.EXPECT().Store(mock.Anything, mock.Anything, mailURL, userID).Return(origErr).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	token, err := svc.GenerateAndStore(s.T().Context(), mailURL, userID)
	s.Error(err)
	s.Contains(err.Error(), "ошибка сохранения данных")
	s.ErrorIs(err, origErr)
	s.Equal("", token)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestGenerateAndStore_Mock_TranslatesErrOriginalURLExists() {
	m := s.createMockStorage()
	m.EXPECT().Store(mock.Anything, mock.Anything, yandexURL, userID).
		Return(repository.NewErrOriginalURLExists(yandexToken, yandexURL)).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	token, err := svc.GenerateAndStore(s.T().Context(), yandexURL, userID)
	var eoue *ErrOriginalURLExists
	s.Require().ErrorAs(err, &eoue)
	s.Equal(yandexToken, eoue.StoredToken)
	s.Equal(yandexURL, eoue.URL)
	s.Equal(yandexToken, token)
	m.AssertExpectations(s.T())
}

// BatchStore

func (s *ShortenerSuite) TestBatchStore_Success() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{CorrelationID: "0", OriginalURL: mailURL},
		model.BatchShortenReqItem{CorrelationID: "1", OriginalURL: someURL},
	}

	res, err := s.svc.BatchStore(s.T().Context(), req, userID)

	s.Require().NoError(err)
	s.Len(res, 2)

	for i := range res {
		s.Equal(req[i].CorrelationID, res[i].CorrelationID)
		s.NoError(encoding.IsValidBase62(res[i].ShortURL))

		url, err := s.svc.Resolve(s.T().Context(), res[i].ShortURL)
		s.Require().NoError(err)
		s.Equal(req[i].OriginalURL, url)
	}
}

func (s *ShortenerSuite) TestBatchStore_Got_Conflicts_On_OriginalURL() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{CorrelationID: "0", OriginalURL: yandexURL}, // словим ErrOriginalURLExists
		model.BatchShortenReqItem{CorrelationID: "1", OriginalURL: someURL},   // тут всё должно отработать хорошо
	}

	res, err := s.svc.BatchStore(s.T().Context(), req, userID)

	s.Require().NoError(err)
	s.Len(res, 2)

	// Конфликтный URL для нулевого элемента: возвращается старый токен
	s.Assert().True(res[0].ConflictedURL)
	s.Assert().Equal(yandexToken, res[0].ShortURL)

	// Чистый первый элемент
	s.Assert().False(res[1].ConflictedURL)

	// В остальном данные валидны
	for i := range res {
		s.Equal(req[i].CorrelationID, res[i].CorrelationID)
		s.NoError(encoding.IsValidBase62(res[i].ShortURL))

		url, err := s.svc.Resolve(s.T().Context(), res[i].ShortURL)
		s.Require().NoError(err)
		s.Equal(req[i].OriginalURL, url)
	}
}

func (s *ShortenerSuite) TestBatchStore_Mock_EmptyRequest_NoStorageCalls() {
	// Мок без ожиданий: любой вызов хранилища привёл бы к панике
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	res, err := svc.BatchStore(s.T().Context(), model.BatchShortenReq{}, userID)
	s.Require().NoError(err)
	s.Empty(res)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestBatchStore_Mock_RetryOfTakenToken() {
	// Ретрай занятого токена: на вторую попытку уходит только
	// несохранённый элемент с сохранением CorrelationID и URL
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	var calls int
	var firstAttemptToken string
	m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).RunAndReturn(
		func(ctx context.Context, batch repository.StoreBatch, uid string) (repository.StoreBatch, error) {
			calls++
			res := make(repository.StoreBatch, 0, len(batch))
			for _, it := range batch {
				if calls == 1 {
					firstAttemptToken = it.Token
					conflicted := it
					conflicted.ConflictedToken = true
					res = append(res, conflicted)
					continue
				}
				res = append(res, it)
			}
			if calls == 1 {
				// Единственный элемент батча — с занятым токеном
				return res, repository.NewErrTokenTaken(firstAttemptToken)
			}
			// Вторая попытка: успех
			return res, nil
		}).Times(2)

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{CorrelationID: "0", OriginalURL: mailURL},
	}

	res, err := svc.BatchStore(s.T().Context(), req, userID)
	s.Require().NoError(err)
	s.Require().Len(res, 1)
	s.Equal("0", res[0].CorrelationID)
	s.NoError(encoding.IsValidBase62(res[0].ShortURL))
	s.False(res[0].ConflictedURL)

	// Токен второй попытки сгенерирован заново
	s.NotEqual(firstAttemptToken, res[0].ShortURL)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestBatchStore_Mock_ExhaustedAttempts_Rollback() {
	// Исчерпание попыток: откат только своих записей,
	// элемент с конфликтом URL (чужая запись) не трогается
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	cfg.MaxStoringAttempts = 2
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	var storedAToken string
	m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).RunAndReturn(
		func(ctx context.Context, batch repository.StoreBatch, uid string) (repository.StoreBatch, error) {
			res := make(repository.StoreBatch, 0, len(batch))
			var takenToken string
			for _, it := range batch {
				if it.CorrelationID == "0" {
					// Успешная запись
					res = append(res, it)
					storedAToken = it.Token
					continue
				}
				// Постоянно занятый токен
				conflicted := it
				conflicted.ConflictedToken = true
				takenToken = it.Token
				res = append(res, conflicted)
			}
			return res, repository.NewErrTokenTaken(takenToken)
		})

	var rollbackTokens []string
	m.EXPECT().DeleteByTokens(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, tokens []string) error {
			rollbackTokens = tokens
			return nil
		}).Once()

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{CorrelationID: "0", OriginalURL: mailURL},
		model.BatchShortenReqItem{CorrelationID: "1", OriginalURL: someURL},
	}

	res, err := svc.BatchStore(s.T().Context(), req, userID)
	s.Error(err)
	s.Contains(err.Error(), "исчерпаны попытки сохранения пакета")
	s.Contains(err.Error(), "не удалось сохранить 1 URL из 2")
	s.Empty(res)

	// Откат забрал только токен своей записи
	s.Require().Len(rollbackTokens, 1)
	s.Equal(storedAToken, rollbackTokens[0])
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestBatchStore_Mock_OtherError_RollbackAndErrorNotSwallowed() {
	// Прочая ошибка посреди пакета: откат своих записей и errors.Join,
	// в котором не теряется ни основная ошибка, ни ошибка отката
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	origErr := errors.New("БД недоступна")
	var storedAToken string
	m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).RunAndReturn(
		func(ctx context.Context, batch repository.StoreBatch, uid string) (repository.StoreBatch, error) {
			for _, it := range batch {
				if it.CorrelationID == "0" {
					storedAToken = it.Token
					// Первый элемент сохранён, но ошибка всё равно приходит
					res := repository.StoreBatch{it}
					return res, origErr
				}
			}
			return repository.StoreBatch{}, origErr
		}).Once()

	rollbackErr := errors.New("не удалось выполнить откат")
	var rollbackTokens []string
	m.EXPECT().DeleteByTokens(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, tokens []string) error {
			rollbackTokens = tokens
			return rollbackErr
		}).Once()

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{CorrelationID: "0", OriginalURL: mailURL},
		model.BatchShortenReqItem{CorrelationID: "1", OriginalURL: someURL},
	}

	res, err := svc.BatchStore(s.T().Context(), req, userID)
	s.Error(err)
	s.Contains(err.Error(), "ошибка сохранения пакета")
	// Ни одна из частей joined-ошибки не потеряна
	s.ErrorIs(err, origErr)
	s.ErrorIs(err, rollbackErr)
	s.Empty(res)

	s.Require().Len(rollbackTokens, 1)
	s.Equal(storedAToken, rollbackTokens[0])
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestBatchStore_Mock_RollbackSkipsConflictedURL() {
	// Один элемент сохранён, второй конфликтует по URL с чужой записью,
	// третий и вовсе не вернулся из хранилища — пакет падает с прочей ошибкой.
	// restoreWithError в откат берёт только свои записи: ConflictedURL-элемент
	// с чужим токеном пропускается, чужая запись не затрагивается
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	// Сентинел в переменной: ErrorIs сравнивает по идентичности, а не по тексту
	storageErr := errors.New("соединение с БД потеряно")

	var storedAToken string
	m.EXPECT().BatchStore(mock.Anything, mock.Anything, userID).RunAndReturn(
		func(ctx context.Context, batch repository.StoreBatch, uid string) (repository.StoreBatch, error) {
			res := make(repository.StoreBatch, 0, len(batch))
			for _, it := range batch {
				switch it.CorrelationID {
				case "0":
					// Чистая вставка — возвращается как сохранённая
					storedAToken = it.Token
					res = append(res, it)
				case "1":
					// Конфликт по URL со старой записью
					conflicted := repository.StoreItem{
						CorrelationID: it.CorrelationID,
						Token:         yandexToken, // старый токен чужой записи
						OriginalURL:   it.OriginalURL,
						ConflictedURL: true,
					}
					res = append(res, conflicted)
				default:
					// Элемент "2" пропал: хранилище до него не дошло
				}
			}
			return res, storageErr
		}).Once()

	var rollbackTokens []string
	m.EXPECT().DeleteByTokens(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, tokens []string) error {
			rollbackTokens = tokens
			return nil
		}).Once()

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{CorrelationID: "0", OriginalURL: mailURL},
		model.BatchShortenReqItem{CorrelationID: "1", OriginalURL: someURL},
		model.BatchShortenReqItem{CorrelationID: "2", OriginalURL: yandexURL},
	}

	_, err = svc.BatchStore(s.T().Context(), req, userID)
	s.Require().Error(err)
	s.Contains(err.Error(), "ошибка сохранения пакета")
	s.ErrorIs(err, storageErr)

	// В откате только своя запись: чужой токен конфликтного элемента не тронут
	s.Require().Len(rollbackTokens, 1)
	s.Equal(storedAToken, rollbackTokens[0])
	s.NotContains(rollbackTokens, yandexToken)
	m.AssertExpectations(s.T())
}

// Ping

func (s *ShortenerSuite) TestPing() {
	m := s.createMockStorage()
	s.createShortenerInstance(m)

	ctx := s.T().Context()

	s.T().Run("успешный ping", func(t *testing.T) {
		m.EXPECT().Ping(mock.Anything).Return(nil).Once()
		s.NoError(s.svc.Ping(ctx))
	})

	s.T().Run("ошибка при ping'е", func(t *testing.T) {
		pfErr := fmt.Errorf("connection timeout")
		m.EXPECT().Ping(mock.Anything).Return(pfErr).Once()
		s.ErrorIs(s.svc.Ping(ctx), pfErr)
	})

	m.AssertExpectations(s.T())
}

// CheckUserExists / CreateUser

func (s *ShortenerSuite) TestUsers_Lifecycle() {
	s.createShortenerInstance(s.storage)

	exists, err := s.svc.CheckUserExists(s.T().Context(), "non-existent-user")
	s.Require().NoError(err)
	s.False(exists)

	uid, err := s.svc.CreateUser(s.T().Context())
	s.Require().NoError(err)
	s.NotEmpty(uid)

	exists, err = s.svc.CheckUserExists(s.T().Context(), uid)
	s.Require().NoError(err)
	s.True(exists)
}

func (s *ShortenerSuite) TestCheckUserExists_Mock_WrapsError() {
	m := s.createMockStorage()
	origErr := errors.New("БД недоступна")
	m.EXPECT().CheckUserExists(mock.Anything, userID).Return(false, origErr).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	_, err = svc.CheckUserExists(s.T().Context(), userID)
	s.Error(err)
	s.Contains(err.Error(), "ошибка хранилища")
	s.ErrorIs(err, origErr)
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestCreateUser_Mock_WrapsError() {
	m := s.createMockStorage()
	origErr := errors.New("БД недоступна")
	m.EXPECT().CreateUser(mock.Anything).Return("", origErr).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	_, err = svc.CreateUser(s.T().Context())
	s.Error(err)
	s.Contains(err.Error(), "ошибка хранилища")
	s.ErrorIs(err, origErr)
	m.AssertExpectations(s.T())
}

// ListUserURLs

func (s *ShortenerSuite) TestListUserURLs_Success() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	res, err := s.svc.ListUserURLs(s.T().Context(), userID)

	s.Require().NoError(err)
	s.Len(res, 1)

	s.Assert().Equal(yandexToken, res[0].ShortURL)
	s.Assert().Equal(yandexURL, res[0].OriginalURL)
}

func (s *ShortenerSuite) TestListUserURLs_Empty_Result() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	res, err := s.svc.ListUserURLs(s.T().Context(), "not-existed-user")

	s.Require().NoError(err)
	s.NotNil(res)
	s.Empty(res)
}

func (s *ShortenerSuite) TestListUserURLs_MultipleURLs() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	// других данных в нём нет

	s.createShortenerInstance(s.storage)

	if err := s.storage.Store(s.T().Context(), mailToken, mailURL, userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
	if err := s.storage.Store(s.T().Context(), goToken, goURL, userID); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}

	res, err := s.svc.ListUserURLs(s.T().Context(), userID)
	s.Require().NoError(err)
	s.Len(res, 3)

	found := make(map[string]string, 3)
	for _, it := range res {
		found[it.ShortURL] = it.OriginalURL
	}
	s.Equal(yandexURL, found[yandexToken])
	s.Equal(mailURL, found[mailToken])
	s.Equal(goURL, found[goToken])
}

func (s *ShortenerSuite) TestListUserURLs_Mock_WrapsError() {
	m := s.createMockStorage()
	origErr := errors.New("БД недоступна")
	m.EXPECT().ListUserURLs(mock.Anything, userID).Return(nil, origErr).Once()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	_, err = svc.ListUserURLs(s.T().Context(), userID)
	s.Error(err)
	s.Contains(err.Error(), "ошибка хранилища")
	s.ErrorIs(err, origErr)
	m.AssertExpectations(s.T())
}

// MarkUserURLsDeleted и очередь удалений

func (s *ShortenerSuite) TestMarkUserURLsDeleted_EmptyBatch() {
	// Пустой батч: ноль обращений к хранилищу (мок без ожиданий)
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	s.NoError(svc.MarkUserURLsDeleted(s.T().Context(), model.TokensToMarkDeleted{}, userID))
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestMarkUserURLsDeleted_EndToEnd_Resolve410() {
	// Полный цикл части 7 через сервис: токен ставится в очередь,
	// процессор доезжает до хранилища, resolve даёт сервисную ErrTokenIsDeleted
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeletionBatchSize = 1 // флаш сразу по первому элементу

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	ctx := s.T().Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartItemsDeletionProcessor(ctx)
	}()

	s.Require().NoError(svc.MarkUserURLsDeleted(ctx, model.TokensToMarkDeleted{yandexToken}, userID))

	// Ждём, пока пакет доедет до хранилища
	deadline := time.Now().Add(2 * time.Second)
	for {
		var errTID *ErrTokenIsDeleted
		_, err := svc.Resolve(ctx, yandexToken)
		if errors.As(err, &errTID) {
			break
		}
		s.Require().False(time.Now().After(deadline), "удаление не доехало до хранилища за отведённое время")
		time.Sleep(10 * time.Millisecond)
	}

	svc.StopItemsDeletionProcessor()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.Fail("процессор удалений не завершился")
	}
}

func (s *ShortenerSuite) TestMarkUserURLsDeleted_QueueOverflow() {
	// Переполнение очереди (буфер 2, отправка 4): первые два элемента
	// попадают в очередь, последние два получают ErrTokenLeftUndeleted
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeletionQueueBuffer = 2
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "dead_letter.jsonl")

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	tokens := model.TokensToMarkDeleted{mailToken, goToken, someToken, yandexToken}
	err = svc.MarkUserURLsDeleted(s.T().Context(), tokens, userID)
	s.Require().Error(err)

	inner := unwrapErrors(err)
	s.Require().Len(inner, 2)
	for i, e := range inner {
		var etlu *ErrTokenLeftUndeleted
		s.Require().ErrorAs(e, &etlu)
		s.Equal(tokens[i+2], etlu.Token)
		s.Equal(userID, etlu.UserID)
	}

	// Необработанные записи можно сбросить в dead-letter
	s.Require().NoError(svc.HandleErrTokenLeftUndeletedAndLogData(err))
	s.checkDeadLetterFile(cfg.DeadLetterLogFile, 2, tokens[2], tokens[3])

	svc.StopItemsDeletionProcessor()
}

func (s *ShortenerSuite) TestMarkUserURLsDeleted_ClosedQueue_NoPanic() {
	// Отправка в закрытую очередь не паникует: весь пакет в ошибки
	cfg := createShortenerTestConfig(s.storage)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	// Двойной стоп безопасен
	svc.StopItemsDeletionProcessor()
	svc.StopItemsDeletionProcessor()

	tokens := model.TokensToMarkDeleted{mailToken, goToken}
	err = svc.MarkUserURLsDeleted(s.T().Context(), tokens, userID)
	s.Require().Error(err)

	inner := unwrapErrors(err)
	s.Require().Len(inner, 2)
	for i, e := range inner {
		var etlu *ErrTokenLeftUndeleted
		s.Require().ErrorAs(e, &etlu)
		s.Equal(tokens[i], etlu.Token)
	}
}

// Процессор удалений

func (s *ShortenerSuite) TestDeletionProcessor_FlushByBatchSize() {
	// Флаш по достижению размера батча: содержимое и порядок сохранены
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	cfg.DeletionBatchSize = 2
	cfg.DeletionQueueFlushTimeout = time.Hour // таймер не вмешивается

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	sent := repository.ToMarkDeletedReqBatch{
		{Token: mailToken, UserID: userID},
		{Token: someToken, UserID: userID},
	}

	flushed := make(chan repository.ToMarkDeletedReqBatch, 1)
	m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, batch repository.ToMarkDeletedReqBatch) error {
			flushed <- batch
			return nil
		}).Once()

	ctx, cancel := context.WithCancel(s.T().Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartItemsDeletionProcessor(ctx)
	}()

	for _, it := range sent {
		s.Require().NoError(svc.MarkUserURLsDeleted(s.T().Context(), model.TokensToMarkDeleted{it.Token}, userID))
	}

	select {
	case got := <-flushed:
		s.Require().Len(got, 2)
		s.Equal(sent, got)
	case <-time.After(2 * time.Second):
		s.Fail("флаш по размеру батча не случился")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.Fail("процессор удалений не завершился")
	}
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestDeletionProcessor_FlushByTimer() {
	// Флаш по таймауту: единственный элемент батча доезжает до хранилища
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	cfg.DeletionBatchSize = 10
	cfg.DeletionQueueFlushTimeout = 100 * time.Millisecond

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	flushed := make(chan repository.ToMarkDeletedReqBatch, 1)
	m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, batch repository.ToMarkDeletedReqBatch) error {
			flushed <- batch
			return nil
		}).Once()

	ctx, cancel := context.WithCancel(s.T().Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartItemsDeletionProcessor(ctx)
	}()

	s.Require().NoError(svc.MarkUserURLsDeleted(s.T().Context(), model.TokensToMarkDeleted{mailToken}, userID))

	select {
	case got := <-flushed:
		s.Require().Len(got, 1)
		s.Equal(mailToken, got[0].Token)
		s.Equal(userID, got[0].UserID)
	case <-time.After(2 * time.Second):
		s.Fail("флаш по таймеру не случился")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.Fail("процессор удалений не завершился")
	}
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestDeletionProcessor_QueueCloseFlushesBatch() {
	// Закрытие очереди доводит накопленный батч до хранилища
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	cfg.DeletionBatchSize = 10
	cfg.DeletionQueueFlushTimeout = time.Hour

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	flushed := make(chan repository.ToMarkDeletedReqBatch, 1)
	m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, batch repository.ToMarkDeletedReqBatch) error {
			flushed <- batch
			return nil
		}).Once()

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartItemsDeletionProcessor(s.T().Context())
	}()

	s.Require().NoError(svc.MarkUserURLsDeleted(s.T().Context(), model.TokensToMarkDeleted{mailToken}, userID))

	// Закрываем очередь: процессор обязан сбросить накопленное и завершиться
	svc.StopItemsDeletionProcessor()

	select {
	case got := <-flushed:
		s.Require().Len(got, 1)
		s.Equal(mailToken, got[0].Token)
	case <-time.After(2 * time.Second):
		s.Fail("закрытие очереди не привело к флашу")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.Fail("процессор удалений не завершился")
	}
	m.AssertExpectations(s.T())
}

func (s *ShortenerSuite) TestDeletionProcessor_DeadLetterOnError() {
	// Ошибка хранилища при флаше: пакет уходит в dead-letter-файл
	m := s.createMockStorage()

	cfg := createShortenerTestConfig(m)
	cfg.DeletionBatchSize = 1 // флаш сразу по первому элементу, без ожидания таймера
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "dead_letter.jsonl")

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	m.EXPECT().MarkUserURLsDeleted(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, batch repository.ToMarkDeletedReqBatch) error {
			return errors.New("ошибка записи в БД")
		}).Once()

	ctx, cancel := context.WithCancel(s.T().Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartItemsDeletionProcessor(ctx)
	}()

	s.Require().NoError(svc.MarkUserURLsDeleted(s.T().Context(), model.TokensToMarkDeleted{mailToken}, userID))

	// Ждём появления записи в dead-letter-файле
	var lines []string
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, readErr := os.ReadFile(cfg.DeadLetterLogFile)
		if readErr == nil {
			lines = splitLines(data)
			if len(lines) == 1 {
				break
			}
		}
		s.Require().False(time.Now().After(deadline), "dead-letter-запись не появилась за отведённое время")
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.Fail("процессор удалений не завершился")
	}

	var item UndeletedItem
	s.Require().NoError(json.Unmarshal([]byte(lines[0]), &item))
	s.Equal(mailToken, item.Token)
	s.Equal(userID, item.UserID)
	s.False(item.Discarded.IsZero())
	m.AssertExpectations(s.T())
}

// Dead-letter-лог

func (s *ShortenerSuite) TestHandleErrTokenLeftUndeletedAndLogData_Nil() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "dead_letter.jsonl")
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	s.NoError(svc.HandleErrTokenLeftUndeletedAndLogData(nil))

	_, readErr := os.ReadFile(cfg.DeadLetterLogFile)
	s.Error(readErr) // файл не создан: писать нечего
	s.checkDeadLetterFile(cfg.DeadLetterLogFile, 0)
}

func (s *ShortenerSuite) TestHandleErrTokenLeftUndeletedAndLogData_SingleError() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "dead_letter.jsonl")
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	err = svc.HandleErrTokenLeftUndeletedAndLogData(NewErrTokenLeftUndeleted(mailToken, userID))
	s.Require().NoError(err)

	s.checkDeadLetterFile(cfg.DeadLetterLogFile, 1, mailToken)
}

func (s *ShortenerSuite) TestHandleErrTokenLeftUndeletedAndLogData_JoinedErrors() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "dead_letter.jsonl")
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	// В joined-ошибке есть посторонняя — она не должна попасть в лог
	joined := errors.Join(
		NewErrTokenLeftUndeleted(mailToken, userID),
		NewErrTokenLeftUndeleted(goToken, userID),
		errors.New("посторонняя ошибка"),
	)

	err = svc.HandleErrTokenLeftUndeletedAndLogData(joined)
	s.Require().NoError(err)

	s.checkDeadLetterFile(cfg.DeadLetterLogFile, 2, mailToken, goToken)
}

func (s *ShortenerSuite) TestHandleErrTokenLeftUndeletedAndLogData_BadPath() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "no_such_dir", "dead_letter.jsonl")
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	err = svc.HandleErrTokenLeftUndeletedAndLogData(NewErrTokenLeftUndeleted(mailToken, userID))
	s.Error(err)
	s.Contains(err.Error(), "не удалось открыть лог ошибок")
}

func (s *ShortenerSuite) TestCleanUp_AllowsNewWrites() {
	// CleanUp закрывает файл, но последующая запись переоткрывает его
	// дозаписью, а не перезаписью
	cfg := createShortenerTestConfig(s.storage)
	cfg.DeadLetterLogFile = filepath.Join(s.T().TempDir(), "dead_letter.jsonl")
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	s.Require().NoError(svc.HandleErrTokenLeftUndeletedAndLogData(NewErrTokenLeftUndeleted(mailToken, userID)))

	svc.CleanUp()

	s.Require().NoError(svc.HandleErrTokenLeftUndeletedAndLogData(NewErrTokenLeftUndeleted(goToken, userID)))

	s.checkDeadLetterFile(cfg.DeadLetterLogFile, 2, mailToken, goToken)
}

func (s *ShortenerSuite) TestNewUndeletedItemsFromReqBatch() {
	batch := repository.ToMarkDeletedReqBatch{
		{Token: mailToken, UserID: userID},
		{Token: goToken, UserID: userID},
	}

	items := NewUndeletedItemsFromReqBatch(batch)
	s.Require().Len(items, 2)
	s.Equal(mailToken, items[0].Token)
	s.Equal(userID, items[0].UserID)
	s.Equal(goToken, items[1].Token)
	s.Equal(userID, items[1].UserID)
	s.True(items[0].Discarded.IsZero()) // Discarded проставляется при записи
}

// Хелперы

// unwrapErrors нормализует ошибку в плоский список: joined — слайс,
// одиночная — список из одного элемента.
func unwrapErrors(err error) []error {
	if err == nil {
		return nil
	}
	if unwrapped, ok := err.(interface{ Unwrap() []error }); ok {
		return unwrapped.Unwrap()
	}
	return []error{err}
}

// splitLines разбирает содержимое файла на непустые строки.
func splitLines(data []byte) []string {
	res := make([]string, 0)
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				res = append(res, string(data[start:i]))
			}
			start = i + 1
		}
	}
	if start < len(data) {
		res = append(res, string(data[start:]))
	}
	return res
}

// checkDeadLetterFile проверяет содержимое dead-letter-файла:
// expectN — ожидаемое число строк, tokens — ожидаемые токены по порядку.
func (s *ShortenerSuite) checkDeadLetterFile(path string, expectN int, tokens ...string) {
	data, err := os.ReadFile(path)
	if expectN == 0 {
		s.Error(err) // файла быть не должно
		return
	}
	s.Require().NoError(err)
	lines := splitLines(data)
	s.Require().Len(lines, expectN)
	for i, line := range lines {
		var item UndeletedItem
		s.Require().NoError(json.Unmarshal([]byte(line), &item))
		if i < len(tokens) {
			s.Equal(tokens[i], item.Token)
		}
		s.Equal(userID, item.UserID)
		s.False(item.Discarded.IsZero())
	}
}

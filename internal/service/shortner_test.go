package service

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

type ShortenerSuite struct {
	suite.Suite
	storage BasicStorage
	svc     *Shortener
}

var (
	userID = "8222be97-0266-40d1-b069-54f29508de43"

	yandexToken = "sPv80uUs"
	yandexURL   = "https://yandex.ru"

	mailToken = "9aF7e72i"
	mailURL   = "https://mail.ru"

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

func createShortenerTestConfig(storage BasicStorage) ShortenerConfig {
	cfg := DefaultShortenerConfig()
	cfg.Storage = storage
	return cfg
}

func (s *ShortenerSuite) createShotnerInstance(storage BasicStorage) {
	cfg := createShortenerTestConfig(storage)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)
	s.Require().NotNil(svc)
	s.svc = svc
}

func (s *ShortenerSuite) TestNewShortener_ValidConfig_CreatesInstance() {
	// Все проверки есть в вызываемом метое
	s.createShotnerInstance(s.storage)
}

func (s *ShortenerSuite) TestNewShortener_InvalidConfig_ReturnsError() {
	badCfg := createShortenerTestConfig(s.storage)
	badCfg.MinTokenLength = 10
	badCfg.MaxTokenLength = 5

	_, err := NewShortener(badCfg)
	s.Error(err)
	s.Contains(err.Error(), errCfgHeader)
}

// generateTokens

func (s *ShortenerSuite) TestGenerateTokens_Size_Fits() {
	s.createShotnerInstance(s.storage)

	for i := 0; i < 1000; i += 50 {
		tokens, err := s.svc.generateTokens(i)
		s.NoError(err)
		s.Equal(i, len(tokens))
	}
}

func (s *ShortenerSuite) TestGenerateTokens_LengthInRange() {
	s.createShotnerInstance(s.storage)

	for i := 0; i < 100; i++ {
		tokens, err := s.svc.generateTokens(1)
		s.NoError(err)
		s.Equal(1, len(tokens))
		tok := tokens[0]
		s.True(len(tok) >= s.svc.Config.MinTokenLength && len(tok) <= s.svc.Config.MaxTokenLength)
		s.NoError(encoding.IsValidBase62(tok))
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

func (FailingReader) Read([]byte) (int, error) {
	return 0, errors.New("ошибка провайдера")
}

func (s *ShortenerSuite) TestGenerateRandomBytes_ErrorWhenProviderFails() {
	cfg := createShortenerTestConfig(s.storage)
	cfg.RandProv = FailingReader{}
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)

	_, err = svc.generateTokens(1)
	s.Error(err)
}

// Resolve

func (s *ShortenerSuite) TestResolve_ExistingToken() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	url, err := s.svc.Resolve(s.T().Context(), yandexToken)
	s.Require().NoError(err)
	s.Equal(yandexURL, url)
}

func (s *ShortenerSuite) TestResolve_ErrTokenNotFound() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	url, err := s.svc.Resolve(s.T().Context(), someToken)
	var errTNF *repository.ErrTokenNotFound
	s.ErrorAs(err, &errTNF)
	s.Equal("", url)
}

// GenerateAndStore

func (s *ShortenerSuite) TestGenerateAndStore_Success() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	token, err := s.svc.GenerateAndStore(s.T().Context(), mailToken, userID)
	s.Require().NoError(err)
	s.NotEmpty(token)
	s.NoError(encoding.IsValidBase62(token))
}

func (s *ShortenerSuite) TestGenerateAndStore_Got_ErrOriginalURLExists() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	token, err := s.svc.GenerateAndStore(s.T().Context(), yandexURL, userID)

	var eoue *repository.ErrOriginalURLExists
	s.Require().ErrorAs(err, &eoue)
	s.Assert().Equal(yandexToken, eoue.StoredToken)
	s.Assert().Equal(yandexURL, eoue.URL)
	s.Assert().Equal(yandexToken, token)

	s.NoError(encoding.IsValidBase62(token))
}

// BatchStore

func (s *ShortenerSuite) TestBatchStore_Success() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{
			CorrelationID: "0",
			OriginalURL:   mailURL,
		},
		model.BatchShortenReqItem{
			CorrelationID: "1",
			OriginalURL:   someURL,
		},
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
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	req := model.BatchShortenReq{
		model.BatchShortenReqItem{
			CorrelationID: "0",
			OriginalURL:   yandexURL, // словим ErrOriginalURLExists
		},
		model.BatchShortenReqItem{
			CorrelationID: "1",
			OriginalURL:   someURL, // тут всё должно отработать хорошо
		},
	}

	res, err := s.svc.BatchStore(s.T().Context(), req, userID)

	s.Require().NoError(err)
	s.Len(res, 2)

	// Конфликтный URL для нулевого элемента
	s.Assert().True(res[0].ConflictedURL)

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

// PingDB

func (s *ShortenerSuite) TestPingDB() {
	rst := new(repository.MockPgStorage)
	s.createShotnerInstance(rst)

	ctx := s.T().Context()

	s.T().Run("успешный ping", func(t *testing.T) {
		rst.On("Ping", ctx).Return(nil).Once()
		s.NoError(s.svc.Ping(ctx))
		rst.AssertExpectations(s.T())
	})

	s.T().Run("ошибка при ping'е", func(t *testing.T) {
		pfErr := fmt.Errorf("connection timeout")
		rst.On("Ping", ctx).Return(pfErr).Once()
		s.ErrorIs(s.svc.Ping(ctx), pfErr)
		rst.AssertExpectations(s.T())
	})
}

// ListUserURLs

func (s *ShortenerSuite) TestListUserURLs_Success() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	//
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	res, err := s.svc.ListUserURLs(s.T().Context(), userID)

	s.Require().NoError(err)
	s.Len(res, 1)

	s.Assert().Equal(yandexToken, res[0].ShortURL)
	s.Assert().Equal(yandexURL, res[0].OriginalURL)
}

func (s *ShortenerSuite) TestListUserURLs_Empty_Result() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище;
	//
	// других данных в нём нет

	s.createShotnerInstance(s.storage)

	res, err := s.svc.ListUserURLs(s.T().Context(), "not-existed-user")

	s.Require().NoError(err)
	s.NotNil(res)
	s.Empty(res)
}

// MarkUserURLsDeleted

func (s *ShortenerSuite) TestMarkUserURLsDeleted_And_Resolve_ErrTokenIsDeleted() {
	// Контекст:
	// (userID) и (yandexToken -> yandexURL) уже лежат в хранилище
	// других данных в нём нет

	// Подготовка данных
	s.createShotnerInstance(s.storage)
	toMarkDeleted := repository.ToMarkDeletedReqBatch{
		repository.ToMarkDeletedReqItem{
			Token:  yandexToken,
			UserID: userID,
		},
	}
	if err := s.storage.MarkUserURLsDeleted(s.T().Context(), toMarkDeleted); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}

	url, err := s.svc.Resolve(s.T().Context(), yandexToken)
	var errTID *repository.ErrTokenIsDeleted
	s.ErrorAs(err, &errTID)
	s.Equal("", url)
}

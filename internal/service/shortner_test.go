package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/mocks"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

type ShortenerSuite struct {
	suite.Suite
	storage BasicStorage
	svc     *Shortener
}

var (
	yandexToken = "sPv80uUs"
	yandexURL   = "https://yandex.ru"

	mailToken = "9aF7e72i"

	someToken = "0000ZZZZ"
)

func (s *ShortenerSuite) SetupTest() {
	s.storage = repository.NewLocalStorage()
	if err := s.storage.Store(context.Background(), yandexToken, model.NewURL(yandexURL)); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

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
		s.True(len(tok) >= s.svc.config.MinTokenLength && len(tok) <= s.svc.config.MaxTokenLength)
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

func (s *ShortenerSuite) TestResolve_ExistingToken() {
	s.createShotnerInstance(s.storage)

	url, err := s.svc.Resolve(context.Background(), yandexToken)
	s.Require().NoError(err)
	s.Equal(yandexURL, url)
}

func (s *ShortenerSuite) TestResolve_ErrTokenNotFound() {
	s.createShotnerInstance(s.storage)

	url, err := s.svc.Resolve(context.Background(), someToken)
	var errTNF *repository.ErrTokenNotFound
	s.ErrorAs(err, &errTNF)
	s.Equal("", url)
}

func (s *ShortenerSuite) TestGenerateAndStore_Success() {
	s.createShotnerInstance(s.storage)

	url, err := s.svc.GenerateAndStore(context.Background(), mailToken)
	s.Require().NoError(err)
	s.NotEqual(model.EmptyURL, url)
	s.NoError(encoding.IsValidBase62(url))
}

func (s *ShortenerSuite) TestPingDB() {
	rst := new(mocks.MockPgStorage)
	s.createShotnerInstance(rst)

	ctx := context.Background()

	s.T().Run("успешный ping", func(t *testing.T) {
		rst.On("Ping", ctx).Return(nil).Once()
		s.NoError(s.svc.PingDB(ctx))
		rst.AssertExpectations(s.T())
	})

	s.T().Run("ошибка при ping'е", func(t *testing.T) {
		pfErr := fmt.Errorf("connection timeout")
		rst.On("Ping", ctx).Return(pfErr).Once()
		s.ErrorIs(s.svc.PingDB(ctx), pfErr)
		rst.AssertExpectations(s.T())
	})
}

func (s *ShortenerSuite) TestWithDB_Positive() {
	s.createShotnerInstance(new(mocks.MockPgStorage))
	s.True(s.svc.WithDB())
}

func (s *ShortenerSuite) TestWithDB_Negative() {
	s.createShotnerInstance(s.storage)
	s.False(s.svc.WithDB())
}

func TestShortenerSuite(t *testing.T) {
	suite.Run(t, new(ShortenerSuite))
}

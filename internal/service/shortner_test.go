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
	if err := s.storage.Store(yandexToken, model.NewURL(yandexURL)); err != nil {
		s.T().Fatalf("Ошибка подготовки тестовых данных: %s", err.Error())
	}
}

func createShortenerTestConfig(storage BasicStorage) ShortenerConfig {
	cfg := DefaultShortenerConfig()
	cfg.Storage = storage
	return cfg
}

func (s *ShortenerSuite) createShotnerInstance() {
	cfg := createShortenerTestConfig(s.storage)
	svc, err := NewShortener(cfg)
	s.Require().NoError(err)
	s.Require().NotNil(svc)
	s.svc = svc
}

func (s *ShortenerSuite) TestNewShortener_ValidConfig_CreatesInstance() {
	s.createShotnerInstance()
}

func (s *ShortenerSuite) TestNewShortener_InvalidConfig_ReturnsError() {
	badCfg := createShortenerTestConfig(s.storage)
	badCfg.MinTokenLength = 10
	badCfg.MaxTokenLength = 5

	_, err := NewShortener(badCfg)
	s.Error(err)
	s.Contains(err.Error(), errCfgHeader)
}

func (s *ShortenerSuite) TestGenerateToken_LengthInRange() {
	s.createShotnerInstance()

	for i := 0; i < 100; i++ {
		token, err := s.svc.GenerateToken()
		s.NoError(err)
		s.NotEmpty(token)
		s.True(len(token) >= s.svc.config.MinTokenLength && len(token) <= s.svc.config.MaxTokenLength)
		s.NoError(encoding.IsValidBase62(token))
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

func (s *ShortenerSuite) TestGenerateToken_RetryOnLengthMismatch_WithFixedProvider() {
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

	_, err = svc.GenerateToken()
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

	_, err = svc.GenerateToken()
	s.Error(err)
}

func (s *ShortenerSuite) TestResolve_ExistingToken() {
	s.createShotnerInstance()

	url, found := s.svc.Resolve(yandexToken)
	s.Require().True(found)
	s.Equal(yandexURL, url)
}

func (s *ShortenerSuite) TestResolve_NotExistingToken() {
	s.createShotnerInstance()

	url, found := s.svc.Resolve(someToken)
	s.Require().False(found)
	s.Equal("", url)
}

func (s *ShortenerSuite) TestGenerateAndStore_Success() {
	s.createShotnerInstance()

	url, err := s.svc.GenerateAndStore(mailToken)
	s.Require().NoError(err)
	s.NotEqual(model.EmptyURL, url)
	s.NoError(encoding.IsValidBase62(url))
}

func (s *ShortenerSuite) TestPingContext() {
	rst := new(mocks.MockPgStorage)

	cfg := createShortenerTestConfig(rst)

	svc, err := NewShortener(cfg)
	s.Require().NoError(err)
	s.Require().NotNil(svc)
	s.svc = svc

	ctx := context.Background()

	s.T().Run("успешный ping", func(t *testing.T) {
		rst.On("PingContext", ctx).Return(nil).Once()
		s.NoError(svc.PingDB(ctx))
		rst.AssertExpectations(s.T())
	})

	s.T().Run("ошибка при ping'е", func(t *testing.T) {
		pfErr := fmt.Errorf("connection timeout")
		rst.On("PingContext", ctx).Return(pfErr).Once()
		s.ErrorIs(svc.PingDB(ctx), pfErr)
		rst.AssertExpectations(s.T())
	})
}

func TestShortenerSuite(t *testing.T) {
	suite.Run(t, new(ShortenerSuite))
}

package service

import (
	"context"

	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/stretchr/testify/mock"
)

type MockShortener struct {
	mock.Mock
}

func (m *MockShortener) GenerateAndStore(ctx context.Context, longURL string) (string, error) {
	args := m.Called(ctx, longURL)
	return args.String(0), args.Error(1)
}

func (m *MockShortener) Resolve(ctx context.Context, token string) (string, error) {
	args := m.Called(ctx, token)
	return args.String(0), args.Error(1)
}

func (m *MockShortener) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockShortener) BatchStore(ctx context.Context, batch model.BatchShortenReq) (model.BatchShortenRes, error) {
	args := m.Called(ctx, batch)
	return args.Get(0).(model.BatchShortenRes), args.Error(1)
}

func (m *MockShortener) GetConfig() ShortenerConfig {
	args := m.Called()
	return args.Get(0).(ShortenerConfig)
}

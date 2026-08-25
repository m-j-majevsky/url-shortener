package repository

import (
	"context"

	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/stretchr/testify/mock"
)

type MockPgStorage struct {
	mock.Mock
}

func (m *MockPgStorage) Ping(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

func (m *MockPgStorage) Store(ctx context.Context, token string, longURL string, userID string) error {
	args := m.Called(ctx, token, longURL, userID)

	return args.Error(0)
}

func (m *MockPgStorage) Resolve(ctx context.Context, token string) (string, error) {
	args := m.Called(ctx, token)

	url := args.String(0)
	err := args.Error(1)

	return url, err
}

func (m *MockPgStorage) BatchStore(ctx context.Context, batch Batch, userID string) (Batch, error) {
	args := m.Called(ctx, batch, userID)

	return args.Get(0).(Batch), args.Error(1)
}

func (m *MockPgStorage) DeleteByTokens(ctx context.Context, tokens []string) error {
	args := m.Called(ctx, tokens)

	return args.Error(0)
}

func (m *MockPgStorage) CheckUserExists(ctx context.Context, userID string) (bool, error) {
	agrs := m.Called(ctx, userID)

	return agrs.Bool(0), agrs.Error(1)
}

func (m *MockPgStorage) CreateUser(ctx context.Context) (string, error) {
	agrs := m.Called(ctx)

	return agrs.String(0), agrs.Error(1)
}

func (m *MockPgStorage) ListUserURLs(ctx context.Context, userID string) (model.UserURLsRes, error) {
	agrs := m.Called(ctx, userID)

	return agrs.Get(0).(model.UserURLsRes), agrs.Error(1)
}

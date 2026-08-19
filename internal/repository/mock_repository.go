package repository

import (
	"context"

	"github.com/stretchr/testify/mock"
)

type MockPgStorage struct {
	mock.Mock
}

func (m *MockPgStorage) Ping(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

func (m *MockPgStorage) Store(ctx context.Context, token string, longURL string) error {
	args := m.Called(ctx, token, longURL)

	return args.Error(0)
}

func (m *MockPgStorage) Resolve(ctx context.Context, token string) (string, error) {
	args := m.Called(ctx, token)

	url := args.String(0)
	err := args.Error(1)

	return url, err
}

func (m *MockPgStorage) BatchStore(ctx context.Context, batch Batch) (Batch, error) {
	args := m.Called(ctx, batch)

	return args.Get(0).(Batch), args.Error(0)
}

func (m *MockPgStorage) DeleteByTokens(ctx context.Context, tokens []string) error {
	args := m.Called(ctx, tokens)

	return args.Error(0)
}

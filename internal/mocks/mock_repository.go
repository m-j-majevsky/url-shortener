package mocks

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

func (m *MockPgStorage) Store(ctx context.Context, token string, longURL model.URL) error {
	args := m.Called(ctx, token, longURL)

	return args.Error(0)
}

func (m *MockPgStorage) Resolve(ctx context.Context, token string) (model.URL, error) {
	args := m.Called(ctx, token)

	url := args.Get(0).(model.URL)
	err := args.Error(1)

	return url, err
}

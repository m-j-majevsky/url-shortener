package mocks

import (
	"context"

	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/stretchr/testify/mock"
)

type MockPgStorage struct {
	mock.Mock
}

func (m *MockPgStorage) PingContext(ctx context.Context) error {
	args := m.Called(ctx)

	return args.Error(0)
}

func (m *MockPgStorage) Store(token string, longURL model.URL) error {
	args := m.Called(token, longURL)

	return args.Error(0)
}

func (m *MockPgStorage) Resolve(token string) (model.URL, bool) {
	args := m.Called(token)

	url := args.Get(0).(model.URL)
	ok := args.Bool(1)

	return url, ok
}

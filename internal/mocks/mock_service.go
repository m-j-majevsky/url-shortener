package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

type MockShortener struct {
	mock.Mock
}

func (m *MockShortener) GenerateAndStore(longURL string) (string, error) {
	args := m.Called(longURL)
	return args.String(0), args.Error(1)
}

func (m *MockShortener) Resolve(token string) (string, bool) {
	args := m.Called(token)
	return args.String(0), args.Bool(1)
}

func (m *MockShortener) PingContext(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

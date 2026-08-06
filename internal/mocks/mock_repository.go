package mocks

import (
	"context"

	"github.com/stretchr/testify/mock"
)

type MockPgStorage struct {
	mock.Mock
}

func (m *MockPgStorage) PingContext(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

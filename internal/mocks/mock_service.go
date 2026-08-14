package mocks

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

func (m *MockShortener) PingDB(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockShortener) WithDB() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *MockShortener) BatchStore(ctx context.Context, batch model.BatchSortenReq) (model.BatchSortenRes, error) {
	args := m.Called(ctx, batch)
	return args.Get(0).(model.BatchSortenRes), args.Error(1)
}

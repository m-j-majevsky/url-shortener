package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type PgStorage struct {
	pool *pgxpool.Pool
}

func NewPgStorage(pool *pgxpool.Pool) *PgStorage {
	return &PgStorage{
		pool: pool,
	}
}

func (s *PgStorage) PingContext(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Сохраняет longURL под токеном. Возвращает ErrTokenTaken, если токен занят.
func (s *PgStorage) Store(token string, longURL model.URL) error {

	// TODO

	return nil
}

func (s *PgStorage) Resolve(token string) (model.URL, bool) {

	// TODO

	return model.EmptyURL, false
}

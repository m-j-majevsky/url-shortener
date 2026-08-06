package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
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

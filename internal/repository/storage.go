package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PgxStorage struct {
	pool *pgxpool.Pool
}

func NewPgxStorage(pool *pgxpool.Pool) *PgxStorage {
	return &PgxStorage{
		pool: pool,
	}
}

func (s *PgxStorage) PingContext(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

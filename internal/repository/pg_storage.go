package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type (
	// Идея введения следующих интерфейсов в этом пакете подсмотрена у Рафаэля Мустафина:
	// https://github.com/Bazys/practicum-webinars/blob/master/videos/repository.go#L20 и далее

	// PgStorage - контракт слоя хранения.
	//
	// Как указывает Рафаэль, _интерфейс_ здесь вводится по следующим причинам:
	//  1. Бизнес-логику (handler) можно тестировать на моке интерфейса, без БД.
	//  2. Можно подменить реализацию (например, in-memory кэш для тестов).
	PgStorage interface {
		Store(ctx context.Context, token string, longURL model.URL) error
		Resolve(ctx context.Context, token string) (model.URL, error)
		Ping(ctx context.Context) error
	}

	// DBTX - минимальный набор методов для работы с БД.
	//
	// Его реализуют _и_ настоящий пул *pgxpool.Pool, _и_ мок pgxmock.PgxConnIface,
	// поэтому репозиторий легко покрывать unit-тестами без живой БД.
	//
	// Важно: возвращаются типы из пакета pgx (pgx.Rows, pgx.Row),
	// а не специфичные для пула - это и позволяет подменять реализацию.
	DBTX interface {
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
		Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
		Ping(ctx context.Context) error
	}

	// pgStorage - реализация PgStorage поверх pgxpool.
	pgStorage struct {
		db DBTX
	}
)

// Убеждаемся на этапе компиляции, что *pgStorage реализует интерфейс PgStorage.
var _ PgStorage = (*pgStorage)(nil)

// NewPgStorage принимает *pgxpool.Pool, который реализует DBTX.
func NewPgStorage(db DBTX) PgStorage {
	return &pgStorage{
		db: db,
	}
}

func (s *pgStorage) Store(ctx context.Context, token string, longURL model.URL) error {
	const q = `INSERT INTO shorten_urls (token, original_url)
	           VALUES ($1, $2)`

	_, err := s.db.Exec(ctx, q, token, longURL.String())

	if err != nil {
		// Различаем нарушение уникальности и прочие ошибки по коду PG.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return NewErrTokenTaken(token)
		}

		return fmt.Errorf("ошибка записи в БД: %w", err)
	}

	return nil
}

func (s *pgStorage) Resolve(ctx context.Context, token string) (model.URL, error) {
	const q = `SELECT original_url
	             FROM shorten_urls
	            WHERE token = $1`

	var url string
	err := s.db.QueryRow(ctx, q, token).Scan(&url)

	if errors.Is(err, pgx.ErrNoRows) {
		return model.EmptyURL, NewErrTokenNotFound(token)
	}

	if err != nil {
		return model.EmptyURL, fmt.Errorf("ошибка запроса URL по токену %s: %w", token, err)
	}

	return model.NewURL(url), nil
}

func (s *pgStorage) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}

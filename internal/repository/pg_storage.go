package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
		Store(ctx context.Context, token string, longURL string) error
		Resolve(ctx context.Context, token string) (string, error)
		Ping(ctx context.Context) error
		BatchStore(ctx context.Context, batch Batch) (Batch, error)
		DeleteByTokens(ctx context.Context, tokens []string) error
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
		Begin(ctx context.Context) (pgx.Tx, error)
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

func (s *pgStorage) Store(ctx context.Context, token string, longURL string) error {
	// Важно: ON CONFLICT срабатывает _только_ для original_url.
	// Конфликты по token будут падать в ошибку, которую мы обработаем ниже.
	const q = `INSERT INTO shorten_urls (token, original_url) 
			   VALUES ($1, $2)
			   ON CONFLICT (original_url) DO UPDATE 
			   SET original_url = EXCLUDED.original_url 
			   RETURNING token`

	var returnedToken string

	row := s.db.QueryRow(ctx, q, token, longURL)
	if err := row.Scan(&returnedToken); err != nil {
		// Различаем по коду PG нарушение уникальности и прочие ошибки
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Сработало ограничение уникальности
			if strings.ToLower(pgErr.ConstraintName) == "shorten_urls_token_key" {
				return fmt.Errorf("%w", NewErrTokenTaken(token))
			}

			// Если вдруг конфликт по другому ограничению, которое мы не ожидали
			return fmt.Errorf("неожиданный конфликт ограничения %s: %w", pgErr.ConstraintName, err)
		}

		// Прочие ошибки
		return fmt.Errorf("ошибка записи в БД: %w", err)
	}

	// Если мы здесь, значит либо INSERT прошел успешно, либо сработал ON CONFLICT (DO UPDATE)
	//
	// Признаком ON CONFLICT считаем отличие returnedToken от item.Token
	if token != returnedToken {
		return fmt.Errorf("%w", NewErrOriginalURLExists(returnedToken, longURL))
	}

	return nil
}

func (s *pgStorage) Resolve(ctx context.Context, token string) (string, error) {
	const q = `SELECT original_url
	           FROM shorten_urls
	           WHERE token = $1`

	var url string
	err := s.db.QueryRow(ctx, q, token).Scan(&url)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w", NewErrTokenNotFound(token))
	}

	if err != nil {
		return "", fmt.Errorf("ошибка запроса URL по токену %s: %w", token, err)
	}

	return url, nil
}

func (s *pgStorage) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
}

// Важно:
// Гарантировать уникальность Batch.Token среди элемемнов параметра batch,
// это ответсвенность вызывающего кода!
func (s *pgStorage) BatchStore(ctx context.Context, batchReq Batch) (Batch, error) {
	if len(batchReq) == 0 {
		return Batch{}, nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("ошибка создания транзакции: %w", err)
	}
	// Откат по умолчанию, будет перезаписан Commit, если всё ок
	defer func() { _ = tx.Rollback(ctx) }()

	const qStoreName = "batch_store"
	// Важно: ON CONFLICT срабатывает _только_ для original_url.
	// Конфликты по token будут падать в ошибку, которую мы обработаем ниже.
	const qStore = `INSERT INTO shorten_urls (token, original_url) 
					VALUES ($1, $2)
					ON CONFLICT (original_url) DO UPDATE 
			        SET original_url = EXCLUDED.original_url 
					RETURNING token`

	if _, err = tx.Prepare(ctx, qStoreName, qStore); err != nil {
		return Batch{}, fmt.Errorf("ошибка подготовки запроса: %w", err)
	}

	batchRes := make(Batch, len(batchReq))
	copy(batchRes, batchReq)

	for i := range batchRes {
		item := &batchRes[i]

		row := tx.QueryRow(ctx, qStoreName, item.Token, item.OriginalURL)

		var returnedToken string
		if err := row.Scan(&returnedToken); err != nil {
			// Различаем по коду PG нарушение уникальности shorten_urls_token_key и прочие ошибки
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Сработало ограничение уникальности
				if strings.ToLower(pgErr.ConstraintName) == "shorten_urls_token_key" {
					item.ConflictedToken = true
					continue
				}

				// Если вдруг конфликт по другому ограничению, которое мы не ожидали - падаем
				return Batch{}, fmt.Errorf("неожиданный конфликт ограничения %s: %w", pgErr.ConstraintName, err)
			}

			// Прочие ошибки считаем критичными для транзакции
			return Batch{}, fmt.Errorf("ошибка записи в БД: %w", err)
		}

		// Если мы здесь, значит либо INSERT прошел успешно, либо сработал ON CONFLICT (DO UPDATE)
		//
		// Признаком ON CONFLICT считаем отличие returnedToken от item.Token
		if item.Token != returnedToken {
			// Такую запись в батче помечаем для дальнейшей обработки в вызывающем коде
			item.ConflictedURL = true
			item.TokenOnConflictedURL = returnedToken
		}
	} // for

	if err := tx.Commit(ctx); err != nil {
		return Batch{}, fmt.Errorf("ошибка завершения транзакции: %w", err)
	}

	return MayBeAddErrors(batchRes)
}

func (s *pgStorage) DeleteByTokens(ctx context.Context, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ошибка создания транзакции: %w", err)
	}
	// Откат из-за ошибки. Commit ниже завершает tx, и тогда Rollback вернёт
	// sql.ErrTxDone — это нормально, ошибку игнорируем.
	defer func() { _ = tx.Rollback(ctx) }()

	const qDelete = "batch_delete"
	if _, err = tx.Prepare(ctx, qDelete, `DELETE FROM shorten_urls WHERE token = $1`); err != nil {
		return fmt.Errorf("ошибка подготовки запроса: %w", err)
	}

	for _, tok := range tokens {
		if _, err = tx.Exec(ctx, qDelete, tok); err != nil {
			return fmt.Errorf("ошибка удаления из БД: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("ошибка завершения транзакции: %w", err)
	}

	return nil
}

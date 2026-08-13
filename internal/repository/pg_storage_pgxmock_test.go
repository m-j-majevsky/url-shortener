package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockPgStorage(t *testing.T) (PgStorage, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	require.NoError(t, err)
	// PgxConnIface реализует интерфейс DBTX (QueryRow/Exec/Ping), поэтому мок
	// можно передать прямо в конструктор репозитория - никакой БД не нужно.
	return NewPgStorage(mock), mock
}

const (
	tok = "wtfTOK"
	url = "https://ya.ru"
)

func TestPgStorage_Ping_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectPing().
		Times(1)

	err := repo.Ping(t.Context())
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Ping_Failed(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectPing().
		WillDelayFor(1 * time.Second).
		WillReturnError(errors.New("no ping"))

	err := repo.Ping(t.Context())
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Resolve_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	rows := mock.NewRows([]string{"original_url"}).AddRow(url)
	mock.ExpectQuery(`SELECT original_url FROM shorten_urls WHERE token = \$1`).
		WithArgs(tok).
		WillReturnRows(rows)

	arUrl, err := repo.Resolve(t.Context(), tok)
	require.NoError(t, err)
	assert.Equal(t, arUrl, model.NewURL(url))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Resolve_ErrTokenNotFound(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectQuery(`SELECT original_url FROM shorten_urls WHERE token = \$1`).
		WithArgs(tok).
		WillReturnError(pgx.ErrNoRows)

	_, err := repo.Resolve(t.Context(), tok)
	var errTNF *ErrTokenNotFound
	assert.ErrorAs(t, err, &errTNF)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Store_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectExec(`INSERT INTO shorten_urls`).
		WithArgs(tok, url).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	err := repo.Store(t.Context(), tok, model.NewURL(url))
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Store_ErrTokenTaken(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectExec(`INSERT INTO shorten_urls`).
		WithArgs(tok, url).
		WillReturnError(&pgconn.PgError{Code: "23505"})

	err := repo.Store(t.Context(), tok, model.NewURL(url))
	var errTT *ErrTokenTaken
	assert.ErrorAs(t, err, &errTT)
	assert.NoError(t, mock.ExpectationsWereMet())
}

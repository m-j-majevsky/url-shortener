package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockPgStorage(t *testing.T) (*pgStorage, pgxmock.PgxConnIface) {
	t.Helper()
	mock, err := pgxmock.NewConn(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	require.NoError(t, err)
	// PgxConnIface реализует интерфейс dbtx (QueryRow/Exec/Ping), поэтому мок
	// можно передать прямо в конструктор репозитория - никакой БД не нужно.
	return NewPgStorage(mock), mock
}

const (
	tok = "wtfTOK"
	url = "https://ya.ru"

	anotherTok = "TOKaNoTHer"
)

// Ping

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

// Resolve

func TestPgStorage_Resolve_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	rows := mock.NewRows([]string{"original_url", "is_deleted"}).AddRow(url, nil)
	mock.ExpectQuery(`SELECT original_url, is_deleted FROM shorten_urls WHERE token = \$1`).
		WithArgs(tok).
		WillReturnRows(rows)

	arURL, err := repo.Resolve(t.Context(), tok)
	require.NoError(t, err)
	assert.Equal(t, arURL, url)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Resolve_ErrTokenNotFound(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectQuery(`SELECT original_url, is_deleted FROM shorten_urls WHERE token = \$1`).
		WithArgs(tok).
		WillReturnError(pgx.ErrNoRows)

	_, err := repo.Resolve(t.Context(), tok)
	var errTNF *ErrTokenNotFound
	assert.ErrorAs(t, err, &errTNF)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Resolve_ErrTokenIsDeleted(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	rows := mock.NewRows([]string{"original_url", "is_deleted"}).AddRow(url, true)
	mock.ExpectQuery(`SELECT original_url, is_deleted FROM shorten_urls WHERE token = \$1`).
		WithArgs(tok).
		WillReturnRows(rows)

	_, err := repo.Resolve(t.Context(), tok)
	var errTID *ErrTokenIsDeleted
	assert.ErrorAs(t, err, &errTID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Store

func TestPgStorage_Store_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	rows := mock.NewRows([]string{"token"}).AddRow(tok)
	mock.ExpectQuery(`INSERT INTO shorten_urls`).
		WithArgs(tok, url, userUIID).WillReturnRows(rows)

	err := repo.Store(t.Context(), tok, url, userID)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Store_ErrTokensTaken(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	mock.ExpectQuery(`INSERT INTO shorten_urls`).
		WithArgs(tok, url, userUIID).
		WillReturnError(&pgconn.PgError{Code: "23505", ConstraintName: "shorten_urls_token_key"})

	err := repo.Store(t.Context(), tok, url, userID)
	var errTT *ErrTokenTaken
	assert.ErrorAs(t, err, &errTT)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Store_ErrOriginalURLExists(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	mock.ExpectQuery(`INSERT INTO shorten_urls`).
		WithArgs(tok, url, userUIID).
		WillReturnRows(mock.NewRows([]string{"token"}).AddRow(anotherTok))

	err := repo.Store(t.Context(), tok, url, userID)
	var eoue *ErrOriginalURLExists
	require.ErrorAs(t, err, &eoue)
	assert.Equal(t, anotherTok, eoue.StoredToken)
	assert.Equal(t, url, eoue.URL)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Store_Violation_Of_Unexpected_Contraint(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	unexpectedConstraint := "some_future_constraint"
	mock.ExpectQuery(`INSERT INTO shorten_urls`).
		WithArgs(tok, url, userUIID).
		WillReturnError(&pgconn.PgError{Code: "23505", ConstraintName: unexpectedConstraint})

	err := repo.Store(t.Context(), tok, url, userID)

	require.Error(t, err)

	var eoue *ErrOriginalURLExists
	require.NotErrorAs(t, err, &eoue)
	var ett *ErrTokenTaken
	require.NotErrorAs(t, err, &ett)

	assert.ErrorContains(t, err, unexpectedConstraint)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPgStorage_Unexpected_DB_Error(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	mock.ExpectQuery(`INSERT INTO shorten_urls`).
		WithArgs(tok, url, userUIID).
		WillReturnError(&pgconn.PgError{Code: pgerrcode.CardinalityViolation})

	err := repo.Store(t.Context(), tok, url, userID)

	require.Error(t, err)

	var eoue *ErrOriginalURLExists
	require.NotErrorAs(t, err, &eoue)
	var ett *ErrTokenTaken
	require.NotErrorAs(t, err, &ett)

	assert.ErrorContains(t, err, pgerrcode.CardinalityViolation)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// BatchStore

func TestBatchStore_Empty_Batch(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	// Никаких ожиданий не нужно

	out, err := repo.BatchStore(t.Context(), StoreBatch{}, userID)
	require.NoError(t, err)
	assert.Empty(t, out)

	// Убеждаемся, что никаких вызовов к БД не было
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchStore_All_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := t.Context()

	mock.ExpectBegin()

	const stmtName = "batch_store"
	const query = `INSERT INTO shorten_urls \(token, original_url, user_id\) 
				   VALUES \(\$1, \$2, \$3\)
				   ON CONFLICT \(original_url\)  
                   DO UPDATE SET original_url = EXCLUDED\.original_url
				   RETURNING token`
	mock.ExpectPrepare(stmtName, query)

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	batchIn := StoreBatch{
		{Token: "tok1", OriginalURL: "https://a.com"},
		{Token: "tok2", OriginalURL: "https://b.com"},
	}
	for _, item := range batchIn {
		rrow := mock.NewRows([]string{"token"}).AddRow(item.Token)
		mock.ExpectQuery(stmtName).
			WithArgs(item.Token, item.OriginalURL, userUIID).
			WillReturnRows(rrow) // возвращаем тот же токен — значит вставка прошла
	}

	mock.ExpectCommit()

	out, err := repo.BatchStore(ctx, batchIn, userID)
	require.NoError(t, err)
	assert.Len(t, out, 2)

	for i := range out {
		assert.Equal(t, batchIn[i].Token, out[i].Token)
		assert.False(t, out[i].ConflictedURL)
		assert.False(t, out[i].ConflictedToken)
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchStore_ErrOriginalURLExists(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := t.Context()

	mock.ExpectBegin()

	const stmtName = "batch_store"
	const query = `INSERT INTO shorten_urls \(token, original_url, user_id\) 
		           VALUES \(\$1, \$2, \$3\)
		           ON CONFLICT \(original_url\)   
                   DO UPDATE SET original_url = EXCLUDED\.original_url
		           RETURNING token`
	mock.ExpectPrepare(stmtName, query)

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	batchIn := StoreBatch{
		{Token: "tok1", OriginalURL: "https://same.com"},   // уже есть
		{Token: "tok2", OriginalURL: "https://unique.com"}, // новый
	}
	existingToken := "existing-tok-for-same-url"

	// 1. Конфликт по original_url: возвращаем существующий токен (не тот, который мы хотели вставить)
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[0].Token, batchIn[0].OriginalURL, userUIID).
		WillReturnRows(mock.NewRows([]string{"token"}).AddRow(existingToken))

	// 2. Успешная вставка
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[1].Token, batchIn[1].OriginalURL, userUIID).
		WillReturnRows(mock.NewRows([]string{"token"}).AddRow(batchIn[1].Token))

	mock.ExpectCommit()

	out, err := repo.BatchStore(ctx, batchIn, userID)

	require.Error(t, err)
	require.Len(t, out, 2)

	// Проверяем ожидания значений полей выходного пакета для записи с конфликтом исходного URL
	itIn, itOut := &batchIn[0], &out[0]
	// Принципиальные маркеры ошибки
	var eoue *ErrOriginalURLExists
	require.ErrorAs(t, err, &eoue)
	assert.True(t, itOut.ConflictedURL)
	assert.Equal(t, existingToken, eoue.StoredToken)
	assert.Equal(t, existingToken, itOut.Token)
	// Корректность остальных полей
	assert.Equal(t, itIn.CorrelationID, itOut.CorrelationID)
	assert.Equal(t, itIn.OriginalURL, itOut.OriginalURL)
	assert.False(t, itOut.ConflictedToken)

	// Проверяем ожидания значений полей выходного пакета для беспроблемной записи
	itIn, itOut = &batchIn[1], &out[1]
	assert.Equal(t, itIn.CorrelationID, itOut.CorrelationID)
	assert.Equal(t, itIn.OriginalURL, itOut.OriginalURL)
	assert.Equal(t, itIn.Token, itOut.Token)
	assert.False(t, itOut.ConflictedToken)
	assert.False(t, itOut.ConflictedURL)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchStore_ConflictToken(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := t.Context()

	mock.ExpectBegin()

	const stmtName = "batch_store"
	const query = `INSERT INTO shorten_urls \(token, original_url, user_id\) 
		           VALUES \(\$1, \$2, \$3\)
		           ON CONFLICT \(original_url\)   
                   DO UPDATE SET original_url = EXCLUDED\.original_url
		           RETURNING token`
	mock.ExpectPrepare(stmtName, query)

	batchIn := StoreBatch{
		{Token: "taken-tok", OriginalURL: "https://new-url.com"},
	}

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	// Эмулируем unique_violation по token
	constraintName := "shorten_urls_token_key"
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[0].Token, batchIn[0].OriginalURL, userUIID).
		WillReturnError(&pgconn.PgError{
			Code:           pgerrcode.UniqueViolation,
			ConstraintName: constraintName,
		})

	// Важно: в текущей реализации BatchStore при конфликте токена
	// мы ставим ConflictedToken=true и делаем continue, поэтому Commit всё равно вызывается.
	mock.ExpectCommit()

	out, err := repo.BatchStore(ctx, batchIn, userID)

	require.Error(t, err)
	require.Len(t, out, 1)

	itIn, itOut := &batchIn[0], &out[0]
	// Принципиальные маркеры ошибки ErrTokenTaken
	var ett *ErrTokenTaken
	require.ErrorAs(t, err, &ett)
	assert.True(t, itOut.ConflictedToken)
	assert.Equal(t, itIn.Token, ett.Token)
	assert.Equal(t, itIn.Token, itOut.Token)
	// Корректность остальных полей
	assert.Equal(t, itIn.CorrelationID, itOut.CorrelationID)
	assert.Equal(t, itIn.OriginalURL, itOut.OriginalURL)
	assert.False(t, itOut.ConflictedURL)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchStore_MixedScenario(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := t.Context()

	mock.ExpectBegin()

	batchIn := StoreBatch{
		{Token: "ok1", OriginalURL: "https://ok1.com"},
		{Token: "conflict-url", OriginalURL: "https://conflict-url.com"},    // конфликт URL
		{Token: "conflict-token", OriginalURL: "https://new-for-token.com"}, // конфликт токена
	}

	const stmtName = "batch_store"
	const query = `INSERT INTO shorten_urls \(token, original_url, user_id\) 
		           VALUES \(\$1, \$2, \$3\)
		           ON CONFLICT \(original_url\)   
                   DO UPDATE SET original_url = EXCLUDED\.original_url
		           RETURNING token`
	mock.ExpectPrepare(stmtName, query)

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	// OK
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[0].Token, batchIn[0].OriginalURL, userUIID).
		WillReturnRows(mock.NewRows([]string{"token"}).AddRow(batchIn[0].Token))

	// Conflict URL: возвращаем другой токен
	existingToken := "existing-for-conflict-url"
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[1].Token, batchIn[1].OriginalURL, userUIID).
		WillReturnRows(mock.NewRows([]string{"token"}).AddRow(existingToken))

	// Conflict Token: ошибка unique_violation
	constraintName := "shorten_urls_token_key"
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[2].Token, batchIn[2].OriginalURL, userUIID).
		WillReturnError(&pgconn.PgError{
			Code:           pgerrcode.UniqueViolation,
			ConstraintName: constraintName,
		})

	mock.ExpectCommit()

	out, err := repo.BatchStore(ctx, batchIn, userID)

	require.Error(t, err)
	assert.Len(t, out, 3)

	// Элемент 0: OK
	itIn, itOut := &batchIn[0], &out[0]
	assert.Equal(t, itIn.CorrelationID, itOut.CorrelationID)
	assert.Equal(t, itIn.OriginalURL, itOut.OriginalURL)
	assert.Equal(t, itIn.Token, itOut.Token)
	assert.False(t, itOut.ConflictedURL)
	assert.False(t, itOut.ConflictedToken)

	// Элемент 1: конфликт URL
	itIn, itOut = &batchIn[1], &out[1]
	var eoue *ErrOriginalURLExists
	require.ErrorAs(t, err, &eoue)
	assert.Equal(t, itIn.OriginalURL, eoue.URL)
	assert.Equal(t, existingToken, eoue.StoredToken)
	assert.Equal(t, existingToken, itOut.Token)
	assert.True(t, itOut.ConflictedURL)
	// Корректность остальных полей
	assert.Equal(t, itIn.CorrelationID, itOut.CorrelationID)
	assert.Equal(t, itIn.OriginalURL, itOut.OriginalURL)
	assert.False(t, itOut.ConflictedToken)

	// Элемент 2: конфликт токена
	itIn, itOut = &batchIn[2], &out[2]
	// Принципиальные маркеры ошибки ErrTokenTaken
	var ett *ErrTokenTaken
	require.ErrorAs(t, err, &ett)
	assert.True(t, itOut.ConflictedToken)
	assert.Equal(t, itIn.Token, ett.Token)
	assert.Equal(t, itIn.Token, itOut.Token)
	// Корректность остальных полей
	assert.Equal(t, itIn.CorrelationID, itOut.CorrelationID)
	assert.Equal(t, itIn.OriginalURL, itOut.OriginalURL)
	assert.False(t, itOut.ConflictedURL)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchStore_TransactionError(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := context.Background()

	mock.ExpectBegin()

	const stmtName = "batch_store"
	const query = `INSERT INTO shorten_urls \(token, original_url, user_id\) 
		           VALUES \(\$1, \$2, \$3\)
		           ON CONFLICT \(original_url\)  
                   DO UPDATE SET original_url = EXCLUDED\.original_url 
		           RETURNING token`
	mock.ExpectPrepare(stmtName, query)

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	batchIn := StoreBatch{
		{Token: "tok", OriginalURL: "https://example.com"},
	}
	// Возвращаем какую-то другую ошибку (не 23505), чтобы транзакция упала
	mock.ExpectQuery(stmtName).
		WithArgs(batchIn[0].Token, batchIn[0].OriginalURL, userUIID).
		WillReturnError(errors.New("some unexpected DB error"))

	// Commit не вызывается, потому что функция вернёт ошибку раньше

	_, err := repo.BatchStore(ctx, batchIn, userID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ошибка записи в БД")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// Важно:
//
// Сценарий "точная копия строки (token, origianl_url) на входе в INSERT" обрабатывается корректно
// за счет ON CONFLICT (original_url) DO UPDATE ... RETURNING token, проверка которого
// выполняется до срабатывания ограничения shorten_urls_token_key, откуда следует обработка
// такой входной строки по сценарию ErrOriginalURLExists, т.е. возврату пользователю существующих данных
// без перезаписи, но со статусом конфликта по исходному URL.

// DeleteByTokens

func TestDeleteByTokens_One_Item_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectBegin()
	const stmtName = "batch_delete"
	const query = `DELETE FROM shorten_urls WHERE token = \$1`
	mock.ExpectPrepare(stmtName, query)
	mock.ExpectExec(stmtName).
		WithArgs(tok).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectCommit()

	assert.NoError(t, repo.DeleteByTokens(t.Context(), []string{tok}))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteByTokens_Several_Items_Success(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	mock.ExpectBegin()

	const stmtName = "batch_delete"
	const query = `DELETE FROM shorten_urls WHERE token = \$1`
	mock.ExpectPrepare(stmtName, query)

	tokens := []string{tok, anotherTok}
	for _, t := range tokens {
		mock.ExpectExec(stmtName).
			WithArgs(t).
			WillReturnResult(pgxmock.NewResult("DELETE", 1))
	}

	mock.ExpectCommit()

	assert.NoError(t, repo.DeleteByTokens(t.Context(), tokens))
}

func TestDeleteByTokens_Empty_Token_Set(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	defer mock.Close(t.Context())

	// Никаких ожиданий не нужно: функция сразу вернёт nil
	assert.NoError(t, repo.DeleteByTokens(t.Context(), []string{}))

	// Убеждаемся, что никаких вызовов к БД не было
	assert.NoError(t, mock.ExpectationsWereMet())
}

// CheckUserExists

func TestPgStorage_CheckUserExists(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := context.Background()

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	// Тест 1: Пользователь существует
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE id = \$1\)`).
		WithArgs(userUIID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))

	exists, err := repo.CheckUserExists(ctx, userID)
	require.NoError(t, err)
	assert.True(t, exists)

	// Тест 2: Пользователь не существует
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE id = \$1\)`).
		WithArgs(userUIID).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	exists, err = repo.CheckUserExists(ctx, userID)
	require.NoError(t, err)
	assert.False(t, exists)

	// Тест 3: Ошибка при запросе
	mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM users WHERE id = \$1\)`).
		WithArgs(userUIID).
		WillReturnError(errors.New("database error"))

	exists, err = repo.CheckUserExists(ctx, userID)
	require.Error(t, err)
	assert.False(t, exists)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// CreateUser

func TestPgStorage_CreateUser(t *testing.T) {
	repo, mock := newMockPgStorage(t)
	ctx := context.Background()

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	// Успешное создание пользователя
	mock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(userUIID))

	createdID, err := repo.CreateUser(ctx)
	require.NoError(t, err)
	assert.Equal(t, userID, createdID)

	// Ошибка при создании
	mock.ExpectQuery(`INSERT INTO users DEFAULT VALUES RETURNING id`).
		WillReturnError(pgx.ErrTxClosed)

	_, err = repo.CreateUser(ctx)
	require.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// ListUserURLs

func TestPgStorage_ListUserURLs(t *testing.T) {
	// Создаем мок-хранилище
	repo, mock := newMockPgStorage(t)
	ctx := context.Background()

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	// Тест 1: Пользователь существует и имеет URL-ы
	t.Run("User exists with URLs", func(t *testing.T) {
		// Настраиваем мок для возврата нескольких URL
		mock.ExpectQuery(`SELECT token, original_url FROM shorten_urls WHERE user_id = \$1`).
			WithArgs(userUIID).
			WillReturnRows(pgxmock.NewRows([]string{"token", "original_url"}).
				AddRow(tok, url).
				AddRow(anotherTok, "https://google.com"))

		urls, err := repo.ListUserURLs(ctx, userID)
		require.NoError(t, err)
		assert.Len(t, urls, 2)
		assert.Equal(t, tok, urls[0].ShortURL)
		assert.Equal(t, url, urls[0].OriginalURL)
		assert.Equal(t, anotherTok, urls[1].ShortURL)
		assert.Equal(t, "https://google.com", urls[1].OriginalURL)
	})

	// Тест 2: Пользователь существует, но URL-ов нет
	t.Run("User exists without URLs", func(t *testing.T) {
		mock.ExpectQuery(`SELECT token, original_url FROM shorten_urls WHERE user_id = \$1`).
			WithArgs(userUIID).
			WillReturnRows(pgxmock.NewRows([]string{"token", "original_url"}))

		urls, err := repo.ListUserURLs(ctx, userID)
		require.NoError(t, err)
		assert.Empty(t, urls)
	})

	// Тест 3: Неверный формат userID
	t.Run("Invalid userID format", func(t *testing.T) {
		_, err := repo.ListUserURLs(ctx, "invalid-id")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "неверный формат ID пользователя")
	})

	// Тест 4: Ошибка базы данных
	t.Run("Database error", func(t *testing.T) {
		mock.ExpectQuery(`SELECT token, original_url FROM shorten_urls WHERE user_id = \$1`).
			WithArgs(userUIID).
			WillReturnError(errors.New("database error"))

		_, err := repo.ListUserURLs(ctx, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ошибка запроса по URL")
	})

	// Тест 5: Пустой userID
	t.Run("Empty userID", func(t *testing.T) {
		_, err := repo.ListUserURLs(ctx, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "неверный формат ID пользователя")
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

// MarkUserURLsDeleted

func TestPgStorage_MarkUserURLsDeleted(t *testing.T) {
	// Создаем мок-хранилище
	repo, mock := newMockPgStorage(t)
	ctx := context.Background()

	var userUIID pgtype.UUID
	require.NoError(t, userUIID.Scan(userID))

	// Тест 1: Пустой батч не обрабатывается в хранилище
	t.Run("Empty batch won't be processed", func(t *testing.T) {
		err := repo.MarkUserURLsDeleted(ctx, ToMarkDeletedReqBatch{})
		require.NoError(t, err)
	})

	// Тест 2: Построение правильного запроса для установки флага is_deleted
	t.Run("Correct UPDATE queue building", func(t *testing.T) {
		sql := regexp.QuoteMeta(`UPDATE shorten_urls AS u 
								 SET is_deleted = TRUE 
							  	 FROM (VALUES ($1, $2::uuid), ($3, $4::uuid)) AS v(token, user_id) 
								 WHERE u.token = v.token AND u.user_id = v.user_id`)
		mock.ExpectExec(sql).
			WithArgs(tok, userUIID, tok+"W", userUIID).
			WillReturnResult(pgxmock.NewResult("UPDATE", 2))

		err := repo.MarkUserURLsDeleted(ctx, ToMarkDeletedReqBatch{
			ToMarkDeletedReqItem{
				Token:  tok,
				UserID: userID,
			},
			ToMarkDeletedReqItem{
				Token:  tok + "W",
				UserID: userID,
			},
		})
		require.NoError(t, err)
	})

	assert.NoError(t, mock.ExpectationsWereMet())
}

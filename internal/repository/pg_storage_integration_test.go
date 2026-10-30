//go:build integration

// Идея реализации тестов подсмотрена у Рафаэля Мустафина:
// https://github.com/Bazys/practicum-webinars/blob/master/videos/repository_integration_test.go
//
// Интеграционные тесты поднимают PostgreSQL в Docker-контейнере.
// Сборочный тег integration гарантирует, что `go test ./...` по умолчанию
// их не запускает, нужен явный `go test -tags=integration`.
//
// Запуск:
//
//	go test -tags=integration -v -run TestPgStorage_Integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/m-j-majevsky/url-shortener/migrations"
)

// testDB — общий пул для всех тестов сьюта (поднимаем контейнер один раз).
var testDB *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()

	// Поднимаем Postgres в Docker. testcontainers сам скачивает образ,
	// назначает случайный порт и ждёт готовности БД.
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		panic("postgres container: " + err.Error())
	}
	// Container lifetime managed by TestMain — Terminate after the suite.
	// (testcontainers also auto-cleans via reaper if process dies.)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic("connection string: " + err.Error())
	}

	// Накатываем миграции (shared-хелпер — его использует и main.go).
	if err := migrations.RunMigrations(ctx, dsn); err != nil {
		panic("migrations: " + err.Error())
	}

	// Пул для запросов приложения. В тестах берём щедрые лимиты,
	// чтобы конкурентный тест на счётчик не упёрся в размер пула.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		panic("parse config: " + err.Error())
	}
	cfg.MaxConns = 20
	testDB, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		panic("pgxpool: " + err.Error())
	}

	code := m.Run()

	testDB.Close()
	_ = container.Terminate(context.Background()) // best-effort cleanup
	os.Exit(code)
}

func newIntegrationPgStorage(t *testing.T) *pgStorage {
	t.Helper()
	require.NotNil(t, testDB, "testDB не инициализирован")

	// Чистим таблицу перед каждым тестом
	_, err := testDB.Exec(t.Context(), "TRUNCATE shorten_urls")
	require.NoError(t, err)

	return NewPgStorage(testDB)
}

func TestPgStorage_Integration_CRUD(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	require.NoError(t, storage.Ping(ctx))

	userID, err := storage.CreateUser(ctx)
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, tok, url, userID))

	res, err := storage.Resolve(ctx, tok)
	require.NoError(t, err)
	assert.Equal(t, url, res)
}

func TestPgStorage_Integration_Resolve_ErrNotFound(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	_, err := storage.Resolve(ctx, tok)
	var errTNF *ErrTokenNotFound
	assert.ErrorAs(t, err, &errTNF)
}

func TestPgStorage_Integration_Store_ErrTokensTaken(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	userID, err := storage.CreateUser(ctx)
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, tok, url, userID))

	err = storage.Store(ctx, tok, "https://google.com", userID)
	var errTT *ErrTokenTaken
	assert.ErrorAs(t, err, &errTT)
}

func TestPgStorage_Integration_Store_ErrOriginalURLExists(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	tok := "wtfTOK"
	url := "https://ya.ru"
	anotherTok := "TOKaNoTHer"

	userID, err := storage.CreateUser(ctx)
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, tok, url, userID))

	err = storage.Store(ctx, anotherTok, url, userID)
	var eoue *ErrOriginalURLExists
	require.ErrorAs(t, err, &eoue)
	assert.Equal(t, tok, eoue.StoredToken)
	assert.Equal(t, url, eoue.URL)
	// Проверки всех полей в деталях представлены в тестах на моке pgxmock
}

// Точная копия строки (тот же токен и тот же URL): контракт требует
// ErrOriginalURLExists (409) с собственным токеном, а не тихий успех.
// Различение обеспечивает xmax-трюк: у чистой вставки xmax = 0,
// после DO UPDATE (даже холостого) — идентификатор транзакции.
// Живая проверка xmax на реальном PostgreSQL.
func TestPgStorage_Integration_Store_Exact_Copy(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	tok := "exactTOK"
	url := "https://exact.copy.ru"

	userID, err := storage.CreateUser(ctx)
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, tok, url, userID))

	err = storage.Store(ctx, tok, url, userID)
	var eoue *ErrOriginalURLExists
	require.ErrorAs(t, err, &eoue)
	assert.Equal(t, tok, eoue.StoredToken)
	assert.Equal(t, url, eoue.URL)

	// Данные не перезаписаны и доступны
	res, err := storage.Resolve(ctx, tok)
	require.NoError(t, err)
	assert.Equal(t, url, res)
}

func TestPgStorage_Integration_ListUserURLs_Success(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	userID, err := storage.CreateUser(ctx)
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, tok, url, userID))

	res, err := storage.ListUserURLs(ctx, userID)
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, tok, res[0].ShortURL)
}

// Мягкое удаление, пересохранение удалённого URL и жёсткое удаление.
// Поведение конфликтов на удалённых строках — это контракт самой БД
// (ON CONFLICT (original_url) срабатывает и на строке с is_deleted = TRUE),
// поэтому оно проверяется только интеграционно, на живом PostgreSQL.
func TestPgStorage_Integration_Deletion_Semantics(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	userID, err := storage.CreateUser(ctx)
	require.NoError(t, err)

	require.NoError(t, storage.Store(ctx, tok, url, userID))

	// Мягкое удаление
	require.NoError(t, storage.MarkUserURLsDeleted(ctx, ToMarkDeletedReqBatch{
		{Token: tok, UserID: userID},
	}))

	// Resolve удалённого токена даёт 410
	_, err = storage.Resolve(ctx, tok)
	var errTID *ErrTokenIsDeleted
	require.ErrorAs(t, err, &errTID)

	// Листинг не содержит удалённых записей
	urls, err := storage.ListUserURLs(ctx, userID)
	require.NoError(t, err)
	assert.Empty(t, urls)

	// Пересохранение удалённого URL даёт ErrOriginalURLExists со СТАРЫМ токеном (409)
	err = storage.Store(ctx, anotherTok, url, userID)
	var eoue *ErrOriginalURLExists
	require.ErrorAs(t, err, &eoue)
	assert.Equal(t, tok, eoue.StoredToken)
	assert.Equal(t, url, eoue.URL)

	// Удалённый токен остаётся занятым: повторное использование — ErrTokenTaken (не 410 и не успех)
	err = storage.Store(ctx, tok, "https://google.com", userID)
	var errTT *ErrTokenTaken
	require.ErrorAs(t, err, &errTT)
	assert.Equal(t, tok, errTT.Token)

	// Точная копия удалённой строки: тоже конфликт со старым токеном,
	// а не успех — xmax-трюк отличает её от чистой вставки
	err = storage.Store(ctx, tok, url, userID)
	require.ErrorAs(t, err, &eoue)
	assert.Equal(t, tok, eoue.StoredToken)
	assert.Equal(t, url, eoue.URL)

	// URL при этом остаётся удалённым: resolve даёт 410
	_, err = storage.Resolve(ctx, tok)
	require.ErrorAs(t, err, &errTID)

	// Жёсткое удаление снимает признак мягкого: после него токен даёт 404, а не 410
	require.NoError(t, storage.DeleteByTokens(ctx, []string{tok}))
	_, err = storage.Resolve(ctx, tok)
	var errTNF *ErrTokenNotFound
	require.ErrorAs(t, err, &errTNF)
}

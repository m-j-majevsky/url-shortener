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
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
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

func newIntegrationPgStorage(t *testing.T) PgStorage {
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

	modelUrl := model.NewURL(url)

	require.NoError(t, storage.Store(ctx, tok, modelUrl))

	res, err := storage.Resolve(ctx, tok)
	require.NoError(t, err)
	assert.Equal(t, modelUrl, res)
}

func TestPgStorage_Integration_Resolve_ErrNotFound(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	_, err := storage.Resolve(ctx, tok)
	var errTNF *ErrTokenNotFound
	assert.ErrorAs(t, err, &errTNF)
}

func TestPgStorage_Integration_Resolve_ErrTokensTaken(t *testing.T) {
	storage := newIntegrationPgStorage(t)
	ctx := t.Context()

	require.NoError(t, storage.Store(ctx, tok, model.NewURL(url)))

	err := storage.Store(ctx, tok, model.URL("https://google.com"))
	var errTT *ErrTokensTaken
	assert.ErrorAs(t, err, &errTT)
}

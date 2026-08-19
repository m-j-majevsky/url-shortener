// Идея реализация подсмотрена у Рафаэля Мустафина
// https://github.com/Bazys/practicum-webinars/blob/master/videos/migrate.go

package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// migrations встраивает миграционные скрипты прямо в бинарник
//
//go:embed *.sql
var migrations embed.FS

// runMigrations накатывает все доступные миграции.
// dsn - строка вида "postgres://user:pass@host:5432/db?sslmode=disable".
func RunMigrations(_ context.Context, dsn string) error {
	src, err := iofs.New(migrations, ".")
	if err != nil {
		return fmt.Errorf("ошибка доступа к источнику даннах миграции: %w", err)
	}

	// Очищаем DSN от лишних фильтров
	if dsn, err = filterDSNToSSLMode(dsn); err != nil {
		return fmt.Errorf("ошибка обработки dsn: %w", err)
	}

	// migrate гоняется через database/sql, а не через нативный pgxpool:
	// драйвер pgx5 (pgx/v5/stdlib) оборачивает pgx в интерфейс sql.DB.
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("ошибка доступа к pgx через интерфейс sql.DB: %w", err)
	}
	defer db.Close()

	// pgxmigrate.WithInstance превращает *sql.DB в database.Driver для migrate.
	dbDriver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("ошибка получения драйвера для миграции: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", dbDriver)
	if err != nil {
		return fmt.Errorf("ошибка создания инстанса миграции: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("ошибка migrate up: %w", err)
	}
	return nil
}

// filterDSNToSSLMode возвращает DSN, в котором из query-параметров
// остаётся только sslmode (если он есть).
// Остальные параметры удаляются.
//
// Нужна для корректной работы pgxmigrate.WithInstance,
// которая не понимает, например, параметр pool_max_conn_idle_time
func filterDSNToSSLMode(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}

	q := u.Query()

	// Читаем значение sslmode (если есть)
	sslmodeVal := ""
	if vals, ok := q["sslmode"]; ok && len(vals) > 0 {
		sslmodeVal = vals[0]
	}

	// Создаём пустой список параметров (все старые теряются)
	q = make(url.Values)

	// Добавляем обратно только sslmode, если он был
	if sslmodeVal != "" {
		q.Set("sslmode", sslmodeVal)
	}

	u.RawQuery = q.Encode()
	return u.String(), nil
}

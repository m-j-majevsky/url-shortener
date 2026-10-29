# ============================================================================
# Makefile для проекта url-shortener
#
# Использование (кратко):
# make all                               # проверит код, соберёт, прогонит тесты
# make cover                             # покажет итоговый процент без моков
# make test-pkg PKG=./internal/handler   # тесты одного пакета
#
# Использование (подробно):
#   make build                           # собрать бинарник в ./bin/shortener
#   make run                             # собрать и запустить (нужны env-переменные или флаги)
#   make test                            # запустить все тесты (без интеграционных)
#   make test-integration                # интеграционные тесты (нужен Docker с postgres:16-alpine)
#   make test-pkg PKG=./internal/handler # запустить тесты конкретного пакета
#   make test-v                          # запустить все тесты с verbose-выводом
#   make cover                           # итоговый процент покрытия (без моков, cmd, migrations)
#   make cover-html                      # HTML-отчёт покрытия (без моков, cmd, migrations)
#   make mocks                           # сгенерировать моки через mockery
#   make tidy                            # go mod tidy
#   make fmt                             # отформатировать код
#   make vet                             # статический анализ
#   make lint                            # fmt + vet
#   make clean                           # удалить бинарник и файлы покрытия
#   make all                             # lint + build + test
#   make bench                           # прогнать бенчмарки (encoding, service, repository, handler)
#   make loadgen                         # собрать генератор нагрузки в ./bin/loadgen
#
# Миграции не требуют отдельной цели: SQL-скрипты встроены в бинарник
# (go:embed в migrations/migrate.go) и накатываются автоматически при старте
# (cmd/shortener/main.go вызывает migrations.RunMigrations с DSN из конфига).
# ============================================================================

BINARY   := bin/shortener
MAIN_PKG := ./cmd/shortener
COVER    := coverage.out
COVER_F  := coverage_filtered.out

# Пакеты, исключаемые из подсчёта покрытия
COVER_EXCLUDE := -e '/mocks/' -e '/cmd/shortener/' -e '/migrations/'

# Версия (из git или fallback)
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -X main.version=$(VERSION)

.PHONY: all build run test test-integration test-pkg test-v cover cover-html mocks tidy fmt vet lint bench loadgen clean

# ---- Комплексные цели ----

all: lint build test

# ---- Сборка ----

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(MAIN_PKG)

run: build
	./$(BINARY)

# ---- Тесты ----

test:
	go test ./...

# Интеграционные тесты живут под build-тегом integration и требуют
# поднятого PostgreSQL (см. internal/repository/pg_storage_integration_test.go)
test-integration:
	go test -tags=integration -cover ./internal/repository/

test-v:
	go test -v ./...

test-pkg:
	go test -v -cover $(PKG)

# ---- Покрытие ----

cover:
	go test -coverprofile=$(COVER) ./...
	grep -v $(COVER_EXCLUDE) $(COVER) > $(COVER_F)
	go tool cover -func=$(COVER_F) | tail -1
	rm -f $(COVER_F)

cover-html:
	go test -coverprofile=$(COVER) ./...
	grep -v $(COVER_EXCLUDE) $(COVER) > $(COVER_F)
	go tool cover -html=$(COVER_F)
	rm -f $(COVER_F)

# ---- Моки ----

mocks:
	mockery

# ---- Модули ----

tidy:
	go mod tidy

# ---- Качество кода ----

fmt:
	go fmt ./...

vet:
	go vet ./...

lint: fmt vet

# ---- Бенчмарки и нагрузочное тестирование ----

# Запуск бенчмарков по пакетам с bench-файлами (юнит-тесты исключены через -run='^$')
bench:
	go test -run='^$$' -bench=. -benchmem ./internal/encoding ./internal/service ./internal/repository ./internal/handler

# Сборка генератора нагрузки (использование: bin/loadgen -mode post -n 20000 -c 50 -warmup 5000)
loadgen:
	go build -o bin/loadgen ./cmd/loadgen

# ---- Очистка ----


clean:
	rm -f $(BINARY) $(COVER) $(COVER_F)

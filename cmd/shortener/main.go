package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/m-j-majevsky/url-shortener/internal/audit"
	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"
	"github.com/m-j-majevsky/url-shortener/migrations"

	"go.uber.org/zap"
)

// version — версия бинарника, заливается при сборке через
// -ldflags "-X main.version=$(VERSION)" (см. Makefile).
var version = "dev"

// auditCloseTimeout — дедлайн ожидания доставки событий аудита при завершении
// работы сервиса. Согласован по порядку величины с ShutdownTimeout: за это время
// воркеры успевают доотправить нормальный объём накопившихся событий, но
// недоступный удалённый приёмник не способен затянуть остановку на минуты.
const auditCloseTimeout = 10 * time.Second

// hardcodedSigningKey — резервный ключ подписания JWT на случай отсутствия
// SIGNING_KEY (см. ErrEmptySigningKey в internal/config). Существует только
// ради автотестов платформы, которые стартуют со значениями по умолчанию.
const hardcodedSigningKey = "jwt-signing-super-secret"

func main() {
	cfg, err := config.LoadApplicationConfig()
	if err != nil && !errors.Is(err, config.ErrEmptySigningKey) {
		log.Fatal(err)
	}
	if errors.Is(err, config.ErrEmptySigningKey) {
		// Т.к. автотесты на платформе должны стартовать со значениями по умолчанию,
		// пока также добавлю ветвь с установкой ключа в явном виде
		cfg.SigningKey = []byte(hardcodedSigningKey)
	}

	parentLogger, err := logger.Initialize(cfg.LogLevel)
	if err != nil {
		log.Fatal(err)
	}
	defer parentLogger.Sync()

	const component = "component"
	mainLogger := parentLogger.With(zap.String(component, "main"))
	serverLogger := parentLogger.With(zap.String(component, "server"))
	handlerLogger := parentLogger.With(zap.String(component, "handler"))
	serviceLogger := parentLogger.With(zap.String(component, "service"))
	auditLogger := parentLogger.With(zap.String(component, "audit"))

	// Параметры сервиса собираются в main: конфиг самодостаточен и не знает о сервисе
	svcConfig := service.DefaultShortenerConfig()

	// Имя файла для логирования необработанных запросов DELETE /api/user/urls
	// Задам хардкодом, пока не придумаю подходящее имя флага или переменной
	svcConfig.DeadLetterLogFile = "emergency_dead_letter.log"
	svcConfig.Logger = serviceLogger

	// Настройка аудита (паттерн «Наблюдатель»).
	// Приёмники создаются только если соответствующий параметр конфигурации задан;
	// иначе соответствующий приёмник аудита отключён.
	var auditObservers []audit.Observer

	if cfg.AuditFile != "" {
		fileSink, err := audit.NewFileSink(cfg.AuditFile)
		if err != nil {
			mainLogger.Fatal(err.Error(), zap.String("event", "opening audit file sink"))
		}
		auditObservers = append(auditObservers, fileSink)
		mainLogger.Info("Audit file sink is on", zap.String("path", cfg.AuditFile))
	}

	if cfg.AuditURL != "" {
		auditObservers = append(auditObservers, audit.NewHTTPSink(cfg.AuditURL))
		mainLogger.Info("Audit remote sink is on", zap.String("url", cfg.AuditURL))
	}

	// Точка диспетчеризации событий аудита; без наблюдателей Notify является no-op
	auditor := audit.NewBroadcaster(auditLogger, auditObservers...)

	// Создаём контекст с возможностью отмены по сигналу для корректного завершения
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var localStorage *repository.LocalStorage
	var saveStateMode bool

	if cfg.DatabaseDSN != "" {
		pool, err := createPool(ctx, cfg.DatabaseDSN)
		if err != nil {
			mainLogger.Fatal(err.Error(), zap.String("event", "creating database connection pool"))
		}
		defer pool.Close()

		// Накатываем миграции
		if err := migrations.RunMigrations(ctx, cfg.DatabaseDSN); err != nil {
			mainLogger.Fatal(err.Error(), zap.String("event", "executing migrations"))
		}

		pgStorage := repository.NewPgStorage(pool)
		svcConfig.Storage = pgStorage
		mainLogger.Info("Remote storage mode is on", zap.String("event", "preparing storage"))
	} else {
		localStorage, saveStateMode, err = loadLocalStorage(mainLogger, cfg.FileStoragePath)
		if err != nil {
			mainLogger.Fatal(err.Error(), zap.String("event", "loading storage state from file"))
		}

		svcConfig.Storage = localStorage
		mainLogger.Info("Local storage mode is on", zap.String("event", "preparing storage"))
	}

	if saveStateMode && cfg.FileStoragePath != "" {
		// Режим сохранения локального хранилища в файл
		defer saveStateDeferred(mainLogger, localStorage, cfg.FileStoragePath)
	}

	service, router, err := makeServiceAndRouter(handlerLogger, svcConfig, cfg, auditor)
	if err != nil {
		mainLogger.Fatal(err.Error(), zap.String("event", "initializing service"))
	}
	defer service.CleanUp()

	// Настраиваем глобальную систему канализации и обработки запросов на удаление записей (DELETE /api/user/urls)
	var backgroundWg sync.WaitGroup
	backgroundWg.Add(1)
	go func() {
		defer backgroundWg.Done()
		service.StartItemsDeletionProcessor(ctx)
	}()

	// Создаем сервер явно, поскольку ссылку на него придется использовать для shutdown'а по сигналу от ОС
	server := &http.Server{
		Addr:    cfg.ServerRunAddress,
		Handler: router,
	}

	// Сам сервер запускаем фоном; ошибка запуска (например, занятый порт) отправляется
	// в канал, из которого её читает main, — иначе ошибка терялась бы в горутине:
	// сервис оставался бы живым, но не отвечающим на запросы.
	serverLogger.Info("Starting server", zap.String("address", server.Addr),
		zap.String("event", "starting server"))
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	// Блокируем main, пока не придёт сигнал или ошибка запуска сервера
	select {
	case <-ctx.Done():
		// Обычный путь: сигнал завершения — переходим к graceful shutdown
	case err := <-errCh:
		// ErrServerClosed сюда дойти не может: Shutdown ещё не вызывался.
		// log.Fatal завершает процесс немедленно, минуя defers; это допустимо:
		// сервер не начал работу, состояние не изменялось, сохранять нечего.
		serverLogger.Fatal(err.Error(), zap.String("event", "starting HTTP server"))
	}

	// Инициируем graceful shutdown
	event := zap.String("event", "shutdown on signal")
	serverLogger.Info("Shutdown signal received", zap.String("cause", context.Cause(ctx).Error()), event)

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownRelease()

	if err := server.Shutdown(shutdownCtx); err != nil {
		serverLogger.Error("Graceful shutdown timed out", zap.Error(err), event)
	} else {
		serverLogger.Info("HTTP server stopped gracefully", event)
	}

	// Закрываем канал для мягкой остановки глобального процесса канализации и обработки
	// запросов на удаление записей (DELETE /api/user/urls)
	service.StopItemsDeletionProcessor()
	mainLogger.Info("Deletion processor input queue channel closed", event)

	// Дожидаемся окончания обработки данных в канале, их сохранения или логирования ошибки
	backgroundWg.Wait()

	// Дожидаемся доставки всех отправленных событий аудита с дедлайном, затем
	// закрываем приёмники (файловый приёмник держит открытый файл, его нужно закрыть).
	// Без дедлайна недоступный удалённый приёмник способен затянуть остановку
	// сервиса на очередь накопившихся событий, умноженную на таймаут одной отправки.
	auditDone := make(chan struct{})
	go func() {
		auditor.Close()
		close(auditDone)
	}()

	select {
	case <-auditDone:
		closeAuditSinks(mainLogger, auditObservers)
	case <-time.After(auditCloseTimeout):
		// Дедлайн истёк: приёмники сознательно не закрываем — воркеры аудита ещё
		// могут отправлять события, и закрытие файла посреди записи дало бы ошибки.
		// Оставшаяся горутина завершится вместе с процессом.
		mainLogger.Error("audit delivery wait timed out, audit sinks left unclosed",
			zap.Duration("timeout", auditCloseTimeout), event)
	}

	mainLogger.Info("Application shutting down gracefully", zap.String("event", "shutdown complete"))
}

// Создаёт настроенный пул соединений и проверяет подключение.
//
// Реализация навеяна подходом Bazys'а:
// https://github.com/Bazys/practicum-webinars/blob/master/videos/main.go: func newPool
//
// Отличие в том, что конфиг я передаю через DSN-строку при запуске.
// Профиль нагрузки ешё не выяснен, и в примере ниже значения параметров произвольные
//
//	./srv.o -l debug \
//	  -d "postgres://url_shortener:SECRET@localhost:30432/url_shortener?sslmode=disable&pool_max_conns=10&pool_max_conn_lifetime=30m&pool_min_conns=2&pool_max_conn_idle_time=5m"
func createPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}

	// Пингуем с таймаутом, чтобы сразу падать, если БД недоступна.
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func loadLocalStorage(log *zap.Logger, storagePath string) (*repository.LocalStorage, bool, error) {
	storage := repository.NewLocalStorage()

	event := zap.String("event", "loading state from file")

	err := storage.LoadFromFile(storagePath)
	if err == nil {
		log.Info("Local storage read successfully", zap.String("path", storagePath), event)
		return storage, true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		log.Info("Local storage file not found, starting with empty storage", zap.String("path", storagePath), event)
		return storage, true, nil
	}

	return nil, false, err
}

// Для сохранения состояния локального хранилища в файл по завершении работы сервиса (предполагается defer вызов)
func saveStateDeferred(log *zap.Logger, storage *repository.LocalStorage, path string) {
	event := zap.String("event", "deferred state saving")
	if err := storage.SaveToFile(path); err != nil {
		log.Error("Failed to save local storage in defer", zap.Error(err), zap.String("path", path), event)
	} else {
		log.Info("Local storage saved in defer", zap.String("path", path), event)
	}
}

func makeServiceAndRouter(log *zap.Logger, svcConfig service.ShortenerConfig, cfg config.ApplicationConfig, auditor audit.Notifier) (*service.Shortener, http.Handler, error) {
	svc, err := service.NewShortener(svcConfig)
	if err != nil {
		return nil, nil, err
	}

	// Роутер принимает сервис через композитный интерфейс handler.Shortener,
	// включающий все мелкие интерфейсы.
	rt := handler.NewRouter(handler.NewRouterParams(cfg, svc, auditor, log))

	return svc, rt, nil
}

// closeAuditSinks закрывает приёмники аудита, требующие освобождения ресурсов
// (например, файловый приёмник держит открытый файл).
// Вызывается после завершения доставки событий.
func closeAuditSinks(log *zap.Logger, observers []audit.Observer) {
	for _, obs := range observers {
		// Интерфейс Observer не обязан быть Closeable — закрываем только тех,
		// кто поддерживает io.Closer
		if closer, ok := obs.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				log.Error("audit sink closing failed", zap.Error(err))
			}
		}
	}
}

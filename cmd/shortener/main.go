package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/m-j-majevsky/url-shortener/internal/config"
	"github.com/m-j-majevsky/url-shortener/internal/handler"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"github.com/m-j-majevsky/url-shortener/internal/service"

	"go.uber.org/zap"
)

func main() {
	cfg, err := config.LoadApplicationConfig()
	if err != nil {
		log.Fatal(err)
	}

	if err := logger.Initialize(cfg.LogLevel); err != nil {
		log.Fatal(err)
	}
	defer logger.Log.Sync()

	var localStorage *repository.LocalStorage
	var saveStateMode bool

	if len(cfg.DatabaseDSN) > 0 {
		pool, err := pgxpool.New(context.Background(), cfg.DatabaseDSN)
		if err != nil {
			logger.Log.Fatal(err.Error(), zap.String("event", "creating database connection pool"))
		}
		defer pool.Close()

		cfg.ServiceConfig.Storage = repository.NewPgStorage(pool)
		logger.Log.Info("Remote storage mode is on", zap.String("event", "preparing storage"))
	} else {
		localStorage, saveStateMode, err = LoadLocalStorage(cfg.FileStoragePath)
		if err != nil {
			logger.Log.Fatal(err.Error(), zap.String("event", "loading storage state from file"))
		}

		cfg.ServiceConfig.Storage = localStorage
		logger.Log.Info("Local storage mode is on", zap.String("event", "preparing storage"))
	}

	handler, err := makeServiceAndRouter(cfg)
	if err != nil {
		logger.Log.Fatal(err.Error(), zap.String("event", "initializing service"))
	}

	// Создаем сервер явно, поскольку ссылку на него придется использовать для shutdown'а по сигналу от ОС
	server := &http.Server{
		Addr:    cfg.ServerRunAddress,
		Handler: handler,
	}

	// Сам сервер запускаем фоном
	go listenAndServe(server)

	// Создаём контекст с возможностью отмены для корректного завершения
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Создаем канал для обработки сигналов от ОС
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Запускаем фоном обработчик сигналов SIGINT/SIGTERM
	go handleShutdownSignals(cfg, cancel, server, sigChan)

	if saveStateMode {
		// Режим сохранения локального хранилища в файл
		defer saveStateDeferred(localStorage, cfg.FileStoragePath)
	}

	// Блокируем main, пока не придёт сигнал
	<-ctx.Done()

	logger.Log.Info("Application shutting down gracefully", zap.String("event", "shutdown complete"))
}

func LoadLocalStorage(storagePath string) (*repository.LocalStorage, bool, error) {
	storage := repository.NewLocalStorage()

	event := zap.String("event", "loading state from file")

	err := storage.LoadFromFile(storagePath)
	if err == nil {
		logger.Log.Info("Local storage read successfully", zap.String("path", storagePath), event)
		return storage, true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		logger.Log.Info("Local storage file not found, starting with empty storage", zap.String("path", storagePath), event)
		return storage, false, nil
	}

	return nil, false, err
}

// Обработка сигналов SIGINT/SIGTERM
func handleShutdownSignals(
	cfg config.ApplicationConfig,
	cancel context.CancelFunc,
	server *http.Server,
	sigChan <-chan os.Signal,
) {
	sig := <-sigChan

	event := zap.String("event", "shutdown on signal")

	logger.Log.Info("Shutdown signal received", zap.String("signal", sig.String()), event)

	// Graceful shutdown сервера
	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownRelease()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Log.Error("Graceful shutdown timed out", zap.Error(err), event)
	} else {
		logger.Log.Info("HTTP server stopped gracefully", event)
	}

	cancel()
}

// Для сохранения состояния локального хранилища в файл по завершении работы сервиса (предполагается defer вызов)
func saveStateDeferred(storage *repository.LocalStorage, path string) {
	event := zap.String("event", "deferred state saving")
	if err := storage.SaveToFile(path); err != nil {
		logger.Log.Error("Failed to save local storage in defer", zap.Error(err), zap.String("path", path), event)
	} else {
		logger.Log.Info("Local storage saved in defer", zap.String("path", path), event)
	}
}

func makeServiceAndRouter(cfg config.ApplicationConfig) (http.Handler, error) {
	svc, err := service.NewShortener(cfg.ServiceConfig)
	if err != nil {
		return nil, err
	}

	handler := logger.WithLogging(
		handler.GzipMiddleware(
			handler.NewRouter(svc, cfg.TargetBaseURL)))

	return handler, nil
}

// Создаем сервис и запускаем HTTP-сервер
func listenAndServe(server *http.Server) {
	event := zap.String("event", "starting server")
	logger.Log.Info("Starting server", zap.String("address", server.Addr), event)
	if err := server.ListenAndServe(); err != nil &&
		// ErrServerClosed возникает при корректном Shutdown
		!errors.Is(err, http.ErrServerClosed) {
		logger.Log.Error(err.Error(), event)
	}
}

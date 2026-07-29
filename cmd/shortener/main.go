package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	storage, err := LoadStorage(cfg.FileStoragePath)
	if err != nil {
		logger.Log.Fatal(err.Error(), zap.String("event", "preparing storage"))
	}
	cfg.ServiceConfig.Storage = storage

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
	go handleShutdownSignals(cfg, cancel, storage, server, sigChan)

	// Запускаем фоном таймер автосохранения
	go saveStateOnTicker(ctx, storage, cfg.FileStoragePath, cfg.SaveStateInterval)

	// Плюс, регистрируем отложенное гарантированное сохранение при любом исходе
	defer saveStateDeferred(storage, cfg.FileStoragePath)

	// Блокируем main, пока не придёт сигнал
	<-ctx.Done()

	logger.Log.Info("Application shutting down gracefully", zap.String("event", "shutdown complete"))
}

func LoadStorage(storagePath string) (*repository.Storage, error) {
	storage := repository.NewStorage()

	event := zap.String("event", "loading state")

	err := storage.LoadFromFile(storagePath)
	if err == nil {
		logger.Log.Info("Storage read successfully", zap.String("path", storagePath), event)
		return storage, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		logger.Log.Info("Storage file not found, starting with empty storage", zap.String("path", storagePath), event)
		return storage, nil
	}

	return nil, err
}

// Обработка сигналов SIGINT/SIGTERM
func handleShutdownSignals(
	cfg config.ApplicationConfig,
	cancel context.CancelFunc,
	storage *repository.Storage,
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

// Таймер автосохранения (тикает каждые snapshotInterval)
func saveStateOnTicker(
	ctx context.Context,
	storage *repository.Storage,
	path string,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := storage.SaveToFile(path); err != nil {
				logger.Log.Error("Failed to save storage snapshot", zap.Error(err), zap.String("path", path), zap.String("event", "saving state on ticker"))
			}
		}
	}
}

// Для гарантированного сохранения при любом выходе (предполагается defer вызов)
func saveStateDeferred(storage *repository.Storage, path string) {
	event := zap.String("event", "deferred state saving")
	if err := storage.SaveToFile(path); err != nil {
		logger.Log.Error("Failed to save storage in defer", zap.Error(err), zap.String("path", path), event)
	} else {
		logger.Log.Info("Storage saved in defer", zap.String("path", path), event)
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

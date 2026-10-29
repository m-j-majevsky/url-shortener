package service

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

// === Бенчмарки сервиса (кандидат оптимизации: генерация и сохранение токенов) ===

// countingStorage — обёртка над локальным хранилищем, которая считает вызовы
// Store. Используется для метрики store-attempts/op: среднего числа попыток
// сохранения на операцию GenerateAndStore (проверка гипотезы о ретраях
// при занятых токенах).
type countingStorage struct {
	*repository.LocalStorage

	storeCalls int
}

func (cs *countingStorage) Store(ctx context.Context, token, longURL, userID string) error {
	cs.storeCalls++
	return cs.LocalStorage.Store(ctx, token, longURL, userID)
}

// newCountingService создаёт сервис со счётчиком попыток сохранения,
// засеивает хранилище preseedURLs записями и прогревает его.
func newCountingService(preseedURLs int) (*Shortener, *countingStorage) {
	inner := repository.NewLocalStorage()
	cs := &countingStorage{LocalStorage: inner}

	cfg := DefaultShortenerConfig()
	cfg.Storage = cs
	// cfg.Logger оставляем nil — сервис использует no-op логгер,
	// а логирование не является предметом этого бенчмарка.

	svc, err := NewShortener(cfg)
	if err != nil {
		panic(fmt.Sprintf("не удалось создать сервис: %v", err))
	}

	ctx := context.Background()
	for i := 0; i < preseedURLs; i++ {
		url := "https://seed.example.com/preseed/" + strconv.Itoa(i)
		if err := inner.Store(ctx, "seedtok"+fmt.Sprintf("%06d", i), url, benchUserID); err != nil {
			panic(fmt.Sprintf("не удалось засеять хранилище: %v", err))
		}
	}
	return svc, cs
}

const benchUserID = "bench-user-0001"

func benchGenerateAndStore(b *testing.B, preseedURLs int) {
	b.Helper()

	svc, cs := newCountingService(preseedURLs)
	ctx := context.Background()
	runtime.GC()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		url := "https://bench.example.com/bench/" + strconv.Itoa(i)
		b.StartTimer()

		if _, err := svc.GenerateAndStore(ctx, url, benchUserID); err != nil {
			b.Fatalf("GenerateAndStore: %v", err)
		}
	}

	b.StopTimer()
	// Метрика: среднее число попыток сохранения на операцию.
	// Ожидание для здорового сервиса — ровно 1.0: коллизии токенов
	// и ретраи должны быть редки; значение > 1 укажет на цену ретраев.
	b.ReportMetric(float64(cs.storeCalls)/float64(b.N), "store-attempts/op")
}

func BenchmarkGenerateAndStore(b *testing.B) {
	for _, preseed := range []struct {
		name string
		n    int
	}{
		{"preseed=0", 0},
		{"preseed=1k", 1_000},
		{"preseed=5k", 5_000},
	} {
		b.Run(preseed.name, func(b *testing.B) {
			benchGenerateAndStore(b, preseed.n)
		})
	}
}

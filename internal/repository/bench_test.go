package repository

// Бенчмарки локального хранилища на мапе.
//
// Ожидание: проверка уникальности original_url идёт по индексу byURL
// за O(1) (см. комментарий в Store, map_based_storage.go), поэтому
// стоимость Store не зависит от объёма уже сохранённых данных; Resolve
// тоже O(1) (поиск по ключу мапы). Параметризация через b.Run по размеру
// preseed: рост чисел Store с ростом preseed сигнализировал бы о
// деградации и опровергал бы это ожидание.

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"testing"
)

// benchUserID не пересекается с пакетной переменной userID
// из map_based_storage_test.go.
const benchUserID = "bench-user-0001"

// sink-переменные пакета защищают вызовы от выкидывания оптимизатором.
var (
	benchStoreErr   error
	benchResolveURL string
	benchResolveErr error
)

// preseedLocalStorage наполняет хранилище n уникальными записями.
// Токены и URL живут в пространстве "seed" — они не пересекаются
// с данными измеряемой фазы, конфликтов Store в preseed нет.
func preseedLocalStorage(b *testing.B, s *LocalStorage, n int) {
	b.Helper()
	for i := 0; i < n; i++ {
		token := "s" + strconv.Itoa(i)
		url := "https://seed.example.com/" + strconv.Itoa(i)
		if err := s.Store(context.Background(), token, url, benchUserID); err != nil {
			b.Fatalf("preseed: %v", err)
		}
	}
}

// benchURLs готовит n уникальных URL для измеряемой фазы.
func benchURLs(b *testing.B, n int) []string {
	b.Helper()
	urls := make([]string, 0, n)
	for i := 0; i < n; i++ {
		urls = append(urls, "https://bench.example.com/"+strconv.Itoa(i))
	}
	return urls
}

// BenchmarkLocalStorage_Store измеряет стоимость сохранения новой записи
// при разных объёмах уже лежащих данных.
func BenchmarkLocalStorage_Store(b *testing.B) {
	for _, preseed := range []int{0, 1000, 5000} {
		b.Run(fmt.Sprintf("preseed_%d", preseed), func(b *testing.B) {
			ctx := context.Background()
			storage := NewLocalStorage()

			// Подготовка целиком до ResetTimer: исключена из замера,
			// отдельно StopTimer/StartTimer не требуются
			if preseed > 0 {
				preseedLocalStorage(b, storage, preseed)
			}
			urls := benchURLs(b, b.N)
			runtime.GC()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				token := "b" + strconv.Itoa(i)
				benchStoreErr = storage.Store(ctx, token, urls[i], benchUserID)
			}
		})
	}
}

// BenchmarkLocalStorage_StoreDuplicate измеряет путь проверки дубликата:
// каждый вызов Store упирается в уже существующий original_url (фаза 409
// в post_run2 loadgen). Токены с префиксом dup не пересекаются с preseed.
func BenchmarkLocalStorage_StoreDuplicate(b *testing.B) {
	for _, preseed := range []int{1000, 5000} {
		b.Run(fmt.Sprintf("preseed_%d", preseed), func(b *testing.B) {
			ctx := context.Background()
			storage := NewLocalStorage()
			preseedLocalStorage(b, storage, preseed)
			urls := benchURLs(b, preseed)
			runtime.GC()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				benchStoreErr = storage.Store(
					ctx, "dup"+strconv.Itoa(i), urls[i%preseed], benchUserID)
			}
		})
	}
}

// BenchmarkLocalStorage_Resolve измеряет стоимость чтения существующей
// записи при разных объёмах хранилища — контрольная группа: должна быть
// практически константой независимо от preseed.
func BenchmarkLocalStorage_Resolve(b *testing.B) {
	for _, preseed := range []int{1000, 5000} {
		b.Run(fmt.Sprintf("preseed_%d", preseed), func(b *testing.B) {
			ctx := context.Background()
			storage := NewLocalStorage()
			preseedLocalStorage(b, storage, preseed)
			runtime.GC()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Детерминированный обход существующих токенов
				benchResolveURL, benchResolveErr = storage.Resolve(
					ctx, "s"+strconv.Itoa(i%preseed))
			}
		})
	}
}

package encoding

import (
	"math/rand"
	"testing"
)

// Sink-переменные защищают вызовы от удаления оптимизатором
// как неиспользуемого кода.
var (
	benchEncodeSink string
	benchValidErr   error
)

// BenchmarkEncodeToBase62 замеряет кодирование 6 байт — ровно столько
// передаёт сервис (BytesToGenerate = 6), это профиль горячего пути.
// Ожидание: аллокации на big.Int (SetBytes, QuoRem) и итоговую строку.
func BenchmarkEncodeToBase62(b *testing.B) {
	// Детерминированный вход: фиксированный сид, объём как в сервисе.
	rng := rand.New(rand.NewSource(1))
	data := make([]byte, 6)
	rng.Read(data)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchEncodeSink = EncodeToBase62(data)
	}
}

// BenchmarkIsValidBase62 замеряет валидацию типичного токена сервиса
// (кодирование 6 случайных байт — как в production-пути).
// Ожидание: копия []byte(s) в цикле и escape-аллокация при ошибке.
func BenchmarkIsValidBase62(b *testing.B) {
	rng := rand.New(rand.NewSource(2))
	data := make([]byte, 6)
	rng.Read(data)
	token := EncodeToBase62(data)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchValidErr = IsValidBase62(token)
	}
}

package base62

import (
	"math/rand"
	"time"
)

// Логика адаптирована из вывода Алисы AI

var (
	r *rand.Rand
)

func init() {
	r = rand.New(rand.NewSource(time.Now().UnixNano()))
}

// Создаёт случайный токен длины tokenLength из base62.
func GenerateToken(tokenLength int) string {
	b := make([]byte, tokenLength)
	for i := 0; i < tokenLength; i++ {
		b[i] = base62Chars[r.Intn(len(base62Chars))]
	}
	return string(b)
}

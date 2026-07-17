package crypto

import (
	"crypto/rand"
)

// Реализация на crypto/rand (потокобезопасна).
type CryptoRandProvider struct{}

func (CryptoRandProvider) Read(p []byte) (int, error) {
	return rand.Read(p)
}

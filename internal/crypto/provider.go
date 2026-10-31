package crypto

import (
	"crypto/rand"
)

// CryptoRandProvider — реализация RandomByteProvider на crypto/rand.
// Потокобезопасна.
type CryptoRandProvider struct{}

// Read заполняет p случайными байтами через crypto/rand и возвращает
// число записанных байт и ошибку.
func (CryptoRandProvider) Read(p []byte) (int, error) {
	return rand.Read(p)
}

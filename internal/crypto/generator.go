// Package crypto предоставляет источники криптографически стойких
// случайных байтов: интерфейс RandomByteProvider, реализацию поверх
// crypto/rand и генерацию ровно n байт от выбранного провайдера.
package crypto

import (
	"errors"
	"io"
)

// RandomByteProvider абстрагирует источник случайных байт — например,
// криптографически стойкий (см. CryptoRandProvider).
type RandomByteProvider interface {
	// Read заполняет p случайными байтами и возвращает число записанных
	// байт и ошибку (контракт io.Reader).
	Read(p []byte) (n int, err error)
}

// GenerateRandomBytes запрашивает у провайдера ровно n случайных байт.
//
// Возвращает слайс длины n. Если n неположительно либо провайдер вернул
// ошибку или меньше n байт, возвращается ошибка.
func GenerateRandomBytes(provider RandomByteProvider, n int) ([]byte, error) {
	if n <= 0 {
		return nil, errors.New("запрашиваемая длина последовательности должна быть положительной")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(provider, b); err != nil {
		return nil, err
	}
	return b, nil
}

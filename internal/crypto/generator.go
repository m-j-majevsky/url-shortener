package crypto

import (
	"errors"
)

// Абстрагирует источник случайных байт.
type RandomByteProvider interface {
	Read(p []byte) (n int, err error)
}

// Запрашивает у провайдера ровно n байт.
func GenerateRandomBytes(provider RandomByteProvider, n int) ([]byte, error) {
	if n <= 0 {
		return nil, errors.New("запрашиваемая длина последовательности должна быть неотрицательной")
	}
	b := make([]byte, n)
	_, err := provider.Read(b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

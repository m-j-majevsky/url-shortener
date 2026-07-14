package base62

import (
	"fmt"
	"testing"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	tests := []int64{0, 1, 61, 62, 3843, 123456789}
	for _, id := range tests {
		t.Run(fmt.Sprintf("Проверка на значении %d", id), func(t *testing.T) {
			token, err := EncodeInt64ToBase62Fixed(id)
			if err != nil {
				t.Fatal(err)
			}
			if len(token) != TokenLength {
				t.Errorf("ожидаемая длина токена %d, фактическая длина %d", TokenLength, len(token))
			}

			decoded, err := DecodeBase62ToInt64(token)
			if err != nil {
				t.Fatal(err)
			}
			if decoded != id {
				t.Errorf("ошибка кодирования-декодирования: %d -> %s -> %d", id, token, decoded)
			}
		})
	}
}

func TestOverflow(t *testing.T) {
	// Подбираем число, которое точно не влезает в 8 символов Base62
	_, err := EncodeInt64ToBase62Fixed(maxCodingValue)
	if err == nil {
		t.Error("ожидается ошибка переполнения")
	}
}

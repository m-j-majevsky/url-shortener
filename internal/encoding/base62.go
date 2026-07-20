package encoding

import (
	"fmt"
	"math/big"
)

const Base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const Base = 62

// Кодирует произвольный слайс байт в строку Base62
func EncodeToBase62(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var n big.Int
	n.SetBytes(data)

	if n.Sign() == 0 {
		return string(Base62Chars[0])
	}

	buf := make([]byte, 0, len(data)*2)

	var q, r big.Int
	zero := big.NewInt(0)
	base := big.NewInt(Base)

	for n.Cmp(zero) > 0 {
		q.QuoRem(&n, base, &r)
		n, q = q, n

		rem := r.Int64()
		buf = append(buf, Base62Chars[rem])
	}

	// Развернуть, потому что собирали с младших разрядов
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}

	return string(buf)
}

// Проверяет, что строка состоит только из символов Base62.
// Возвращает nil при успехе. При ошибке возвращает описание с позицией и символом.
// Пустая строка считается невалидной.
func IsValidBase62(s string) error {
	if s == "" {
		return fmt.Errorf("пустая строка не является валидным Base62 токеном")
	}

	for i, b := range []byte(s) {
		switch {
		case b >= '0' && b <= '9':
			continue
		case b >= 'A' && b <= 'Z':
			continue
		case b >= 'a' && b <= 'z':
			continue
		default:
			return fmt.Errorf(
				"недопустимый символ Base62 %q в позиции %d токена %q", string(b), i, s)
		}
	}

	return nil
}

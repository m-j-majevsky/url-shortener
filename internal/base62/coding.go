package base62

import (
	"errors"
	"fmt"
	"strings"
)

const (
	base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base        = 62
	TokenLength = 8
)

type ErrInvalidChar struct {
	r rune
}

func (e ErrInvalidChar) Error() string {
	return fmt.Sprintf("base62: недопустимый Base62-символ: %c", e.r)
}

func NewErrInvalidChar(r rune) ErrInvalidChar {
	return ErrInvalidChar{r}
}

var (
	// Фактически константы со "сложным" сценарием инициализации, выполняемым в init
	charToValue    [128]int8
	maxCodingValue int64

	ErrNegativeIDNotAlowed = errors.New("base62: отрицательный аргумент не допустим")
	ErrEncodingOverflow    = fmt.Errorf("base62: ошибка переполнения для целевого кода из %d Base62-символов", TokenLength)
)

func init() {
	for i := range charToValue {
		charToValue[i] = -1
	}
	for i, r := range base62Chars {
		if r < 128 {
			charToValue[r] = int8(i)
		}
	}
	maxCodingValue = int64(1)
	for i := 0; i < TokenLength; i++ {
		maxCodingValue *= base
	}
}

// Кодирует в base62-строку фиксированной длины,
// дополняя нулями слева до 8 символов
func EncodeInt64ToBase62Fixed(n int64) (string, error) {
	if n < 0 {
		return "", ErrNegativeIDNotAlowed
	}

	if n >= maxCodingValue {
		return "", ErrEncodingOverflow
	}

	if n == 0 {
		b := make([]byte, TokenLength)
		for i := range b {
			b[i] = '0'
		}
		return string(b), nil
	}

	buf := make([]byte, 0, TokenLength)
	tmp := n

	for tmp > 0 {
		rem := tmp % base
		buf = append(buf, base62Chars[rem])
		tmp /= base
	}

	// развернуть
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}

	// дополнить нулями слева
	if len(buf) < TokenLength {
		prefix := make([]byte, TokenLength-len(buf))
		for i := range prefix {
			prefix[i] = '0'
		}
		buf = append(prefix, buf...)
	}

	return string(buf), nil
}

func DecodeBase62ToInt64(s string) (int64, error) {
	var n int64
	baseVal := int64(base)

	for _, r := range s {
		var val int64
		if r < 128 {
			v := charToValue[r]
			if v == -1 {
				return 0, NewErrInvalidChar(r)
			}
			val = int64(v)
		} else {
			// fallback (не нужен при валидных токенах)
			i := strings.IndexRune(base62Chars, r)
			if i == -1 {
				return 0, NewErrInvalidChar(r)
			}
			val = int64(i)
		}
		n = n*baseVal + val
	}
	return n, nil
}

package base62

import (
	"fmt"
)

// Логика адаптирована из вывода Алисы AI

type Base62Error struct {
	Input   string
	BadChar rune
	Message string
}

func (e *Base62Error) Error() string {
	return e.Message
}

func ValidateBase62(s string) *Base62Error {
	if s == "" {
		return &Base62Error{
			Input:   s,
			BadChar: 0,
			Message: "пустая строка не является допустимым base62-токеном",
		}
	}

	for _, r := range s {
		if r >= 128 {
			return &Base62Error{
				Input:   s,
				BadChar: r,
				Message: fmt.Sprintf("недопустимый символ (не ASCII): %q", r),
			}
		}
		if !base62Set[r] {
			return &Base62Error{
				Input:   s,
				BadChar: r,
				Message: fmt.Sprintf("недопустимый base62-символ: %q", r),
			}
		}
	}

	return nil
}

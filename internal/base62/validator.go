package base62

import (
	"fmt"
)

// Логика адаптирована из вывода Алисы AI

// Фактическая константа - значение вычисляется в init
var base62Set [128]bool

func init() {
	for _, c := range base62Chars {
		if c < 128 {
			base62Set[c] = true
		}
	}
}

type ValidationError struct {
	message string
}

func (e ValidationError) Error() string {
	return e.message
}

func NewValidationError(msg string) ValidationError {
	return ValidationError{msg}
}

func ValidateBase62(s string) error {
	if s == "" {
		return ValidationError{"пустая строка не является допустимым base62-токеном"}
	}

	for _, r := range s {
		if r >= 128 {
			return ValidationError{fmt.Sprintf("недопустимый символ (не ASCII): %q", r)}
		}
		if !base62Set[r] {
			return ValidationError{fmt.Sprintf("недопустимый base62-символ: %q", r)}
		}
	}

	return nil
}

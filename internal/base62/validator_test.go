package base62_test

import (
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
)

func TestValidateBase62(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		passed bool
	}{
		{
			name:   "Проверка на успешность валидации base62-строки",
			s:      "AsD0h8Ds",
			passed: true,
		},
		{
			name:   "Проверка на невалидность пустой строки",
			s:      "",
			passed: false,
		},
		{
			name:   "Проверка на недопустимость не base62-символов ASCII",
			s:      "a_String",
			passed: false,
		},
		{
			name:   "Проверка на недопустимость не base62-символов не из ASCII",
			s:      "aΩString",
			passed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := base62.ValidateBase62(tt.s)
			if tt.passed != (err == nil) {
				t.Errorf("ошибка валидации: %v", err)
			}
		})
	}
}

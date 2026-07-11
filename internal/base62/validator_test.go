package base62_test

import (
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
)

func TestValidateBase62(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want *base62.Base62Error
	}{
		{
			name: "Проверка на успешной валидации base62-строки",
			s:    "AsD0h8Ds",
			want: nil,
		},
		{
			name: "Проверка на невалидность пустой строки",
			s:    "",
			want: &base62.Base62Error{
				Input:   "",
				BadChar: 0,
				Message: "пустая строка не является допустимым base62-токеном",
			},
		},
		{
			name: "Проверка на недопустимость на base62-символов ASCII",
			s:    "a_String",
			want: &base62.Base62Error{
				Input:   "a_String",
				BadChar: '_',
				Message: "недопустимый base62-символ: '_'",
			},
		},
		{
			name: "Проверка на недопустимость на base62-символов не из ASCII",
			s:    "aΩString",
			want: &base62.Base62Error{
				Input:   "aΩString",
				BadChar: 'Ω',
				Message: "недопустимый символ (не ASCII): 'Ω'",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base62.ValidateBase62(tt.s)
			if (tt.want == nil && got != nil) ||
				(tt.want != nil && got == nil) ||
				(tt.want != nil && got.Message != tt.want.Message) {
				t.Errorf("ValidateBase62() = %v, want %v", got, tt.want)
			}
		})
	}
}

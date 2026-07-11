package base62_test

import (
	"testing"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
)

func TestGenerateToken(t *testing.T) {
	tests := []struct {
		name        string
		tokenLength int
		want        string
	}{
		{
			name:        "Запрос токена нулевой длины дает на выходе пустую строку",
			tokenLength: 0,
			want:        "<пустая строка>",
		},
		{
			name:        "Запрос токена ненулевой длины дает на выходе строку base62-символов",
			tokenLength: 8,
			want:        "<строка base62-символов>",
		},
		{
			name:        "Запрос токена фиксированной положительной длины дает на выходе строку запрошенной длины",
			tokenLength: 16,
			want:        "<строка ровно из 16 base62-символов>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := base62.GenerateToken(tt.tokenLength)
			// TODO: update the condition below to compare got with tt.want.
			if tt.tokenLength != len(got) ||
				tt.tokenLength > 0 && base62.ValidateBase62(got) != nil {
				t.Errorf("GenerateToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

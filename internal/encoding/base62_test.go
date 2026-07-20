package encoding

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EncodingSuite struct {
	suite.Suite
}

func TestEncodingSuite(t *testing.T) {
	suite.Run(t, new(EncodingSuite))
}

// --- Тесты EncodeToBase62 ---

func (s *EncodingSuite) TestEncodeToBase62_EmptySlice_ReturnsEmptyString() {
	result := EncodeToBase62([]byte{})
	assert.Empty(s.T(), result)
}

func (s *EncodingSuite) TestEncodeToBase62_AllZeros_ReturnsSingleZero() {
	// Все нули -> big.Int = 0 -> должен вернуть "0"
	data := []byte{0, 0, 0}
	result := EncodeToBase62(data)
	assert.Equal(s.T(), "0", result)
}

func (s *EncodingSuite) TestEncodeToBase62_SingleByte_SmallValue() {
	// 1 -> "1"
	data := []byte{1}
	result := EncodeToBase62(data)
	assert.Equal(s.T(), "1", result)
}

func (s *EncodingSuite) TestEncodeToBase62_SingleByte_MaxBase62Char() {
	// Base62Chars[61] = 'z'
	data := []byte{61} // интерпретируется как число 61
	result := EncodeToBase62(data)
	assert.Equal(s.T(), "z", result)
}

func (s *EncodingSuite) TestEncodeToBase62_TwoBytes_KnownValue() {
	// Возьмём число, которое легко проверить вручную.
	// Например, 62 в десятичной = "10" в Base62.
	// В байтах: 62 = [0, 62] в big-endian, но SetBytes трактует слайс как big-endian.
	n := 62
	data := []byte{byte(n >> 8), byte(n)}
	result := EncodeToBase62(data)
	assert.Equal(s.T(), "10", result)
}

func (s *EncodingSuite) TestEncodeToBase62_RoundTrip_WithRandomishBytes() {
	// Генерируем несколько случайных (но детерминированных) байтов, кодируем,
	// затем декодировать не будем (нет декодера), но проверим, что результат валиден.
	data := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}
	token := EncodeToBase62(data)

	require.NotEmpty(s.T(), token)
	err := IsValidBase62(token)
	require.NoError(s.T(), err, "Сгенерированный токен должен быть валидным Base62")
}

// --- Тесты IsValidBase62 ---

func (s *EncodingSuite) TestIsValidBase62_ValidString_ReturnsNil() {
	validStrings := []string{
		"0",
		"z",
		"A",
		"a",
		"123ABCxyz",
		Base62Chars, // вся строка алфавита
	}

	for _, v := range validStrings {
		s.Run(fmt.Sprintf("valid_%s", v), func() {
			err := IsValidBase62(v)
			assert.NoError(s.T(), err)
		})
	}
}

func (s *EncodingSuite) TestIsValidBase62_EmptyString_ReturnsError() {
	err := IsValidBase62("")
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "пустая строка")
}

func (s *EncodingSuite) TestIsValidBase62_InvalidChar_ReturnsErrorWithPosition() {
	tests := []struct {
		input    string
		badChar  rune
		badIndex int
	}{
		{"123@456", '@', 3},
		{"abc!def", '!', 3},
		{"hello#world", '#', 5},
		{"000$", '$', 3},
	}

	for _, tt := range tests {
		s.Run(fmt.Sprintf("invalid_%s", tt.input), func() {
			err := IsValidBase62(tt.input)
			require.Error(s.T(), err)
			msg := err.Error()
			assert.Contains(s.T(), msg, fmt.Sprintf("%q", string(tt.badChar)))
			assert.Contains(s.T(), msg, fmt.Sprintf("в позиции %d", tt.badIndex))
		})
	}
}

func (s *EncodingSuite) TestIsValidBase62_UnicodeInvalid_ReturnsError() {
	// Не-ASCII символ тоже должен отвергаться
	err := IsValidBase62("abcα") // α — греческая буква
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "недопустимый символ Base62")
}

func (s *EncodingSuite) TestIsValidBase62_Whitespace_ReturnsError() {
	// Пробелы не входят в Base62
	err := IsValidBase62("abc def")
	require.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "недопустимый символ Base62")
}

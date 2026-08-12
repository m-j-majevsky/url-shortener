package repository

import (
	"fmt"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type (
	itemRepr struct {
		UUID        string    `json:"uuid"`
		ShortURL    string    `json:"short_url"`
		OriginalURL model.URL `json:"original_url"`
	}

	storageRepr []itemRepr

	// Ошибка, когда токен уже занят
	ErrTokenTaken struct {
		Token string
	}

	// Ошибка, если запрашиваемый токен не найден
	ErrTokenNotFound struct {
		Token string
	}
)

func NewErrTokenTaken(tok string) *ErrTokenTaken {
	return &ErrTokenTaken{
		Token: tok,
	}
}

func (e *ErrTokenTaken) Error() string {
	return fmt.Sprintf("токен %s занят", e.Token)
}

func NewErrTokenNotFound(tok string) *ErrTokenNotFound {
	return &ErrTokenNotFound{
		Token: tok,
	}
}

func (e *ErrTokenNotFound) Error() string {
	return fmt.Sprintf("токен %s не найден", e.Token)
}

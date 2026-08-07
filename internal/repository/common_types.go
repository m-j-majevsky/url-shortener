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
)

func NewErrTokenTaken(tok string) *ErrTokenTaken {
	return &ErrTokenTaken{
		Token: tok,
	}
}

func (e *ErrTokenTaken) Error() string {
	return fmt.Sprintf("токен %q занят", e.Token)
}

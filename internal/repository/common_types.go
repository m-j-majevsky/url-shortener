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

	BatchItem struct {
		CorrelationID string
		Token         string
		OriginalURL   model.URL
	}

	Batch []BatchItem
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

func NewBatch(req model.BatchSortenReq) Batch {
	result := make(Batch, len(req))
	for i := range req {
		result[i].CorrelationID = req[i].CorrelationID
		result[i].OriginalURL = req[i].OriginalURL
		// result[i].Token остается пустым, пока токен не будет явно предоставлен сервисом
	}
	return result
}

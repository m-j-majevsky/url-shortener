package repository

import (
	"fmt"
	"strings"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type (
	itemRepr struct {
		UUID        string    `json:"uuid"`
		ShortURL    string    `json:"short_url"`
		OriginalURL model.URL `json:"original_url"`
	}

	storageRepr []itemRepr

	// Ошибка, когда токены уже заняты
	ErrTokensTaken struct {
		Tokens []string
	}

	// Ошибка, если запрашиваемый токен не найден
	ErrTokenNotFound struct {
		Token string
	}

	BatchItem struct {
		CorrelationID   string
		Token           string
		OriginalURL     model.URL
		ConflictedToken bool
	}

	Batch []BatchItem
)

func NewErrTokensTaken(tokens []string) *ErrTokensTaken {
	return &ErrTokensTaken{
		Tokens: tokens,
	}
}

func (e *ErrTokensTaken) Error() string {
	return fmt.Sprintf("занятые токены: %s", strings.Join(e.Tokens, ", "))
}

func NewBatch(req model.BatchSortenReq) Batch {
	result := make(Batch, len(req))
	for i := range req {
		result[i].CorrelationID = req[i].CorrelationID
		result[i].OriginalURL = req[i].OriginalURL
	}
	return result
}

func CollectConflictedTokens(batch Batch) []string {
	res := make([]string, 0, len(batch))
	for _, it := range batch {
		if it.ConflictedToken {
			res = append(res, it.Token)
		}
	}
	return res
}

func MayBeAddErrTokenTaken(batch Batch) (Batch, error) {
	var err error
	ctoks := CollectConflictedTokens(batch)
	if len(ctoks) > 0 {
		err = NewErrTokensTaken(ctoks)
	}
	return batch, err
}

func NewErrTokenNotFound(tok string) *ErrTokenNotFound {
	return &ErrTokenNotFound{
		Token: tok,
	}
}

func (e *ErrTokenNotFound) Error() string {
	return fmt.Sprintf("токен %s не найден", e.Token)
}

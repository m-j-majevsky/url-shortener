package repository

import (
	"errors"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type (
	BatchItem struct {
		CorrelationID   string
		Token           string
		OriginalURL     string
		ConflictedToken bool
		ConflictedURL   bool
	}

	Batch []BatchItem
)

// Методы и утилиты для работы с Batch

func NewBatch(req model.BatchShortenReq) Batch {
	result := make(Batch, len(req))
	for i := range req {
		result[i].CorrelationID = req[i].CorrelationID
		result[i].OriginalURL = req[i].OriginalURL
	}
	return result
}

func MayBeAddErrors(batch Batch) (Batch, error) {
	errs := make([]error, 0, len(batch))

	for _, it := range batch {
		if it.ConflictedToken {
			errs = append(errs, NewErrTokenTaken(it.Token))
		}

		if it.ConflictedURL {
			errs = append(errs, NewErrOriginalURLExists(it.Token, it.OriginalURL))
		}
	}

	return batch, errors.Join(errs...)
}

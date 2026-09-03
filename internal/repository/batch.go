package repository

import (
	"errors"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type (
	StoreItem struct {
		CorrelationID   string
		Token           string
		OriginalURL     string
		ConflictedToken bool
		ConflictedURL   bool
	}

	StoreBatch []StoreItem

	ToMarkDeletedReqItem struct {
		Token  string `json:"token"`
		UserID string `json:"user_id"`
	}

	ToMarkDeletedReqBatch []ToMarkDeletedReqItem
)

// Методы и утилиты для работы с StoreBatch

func NewStoreBatch(req model.BatchShortenReq) StoreBatch {
	result := make(StoreBatch, len(req))
	for i := range req {
		result[i].CorrelationID = req[i].CorrelationID
		result[i].OriginalURL = req[i].OriginalURL
	}
	return result
}

func MayBeAddErrors(batch StoreBatch) (StoreBatch, error) {
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

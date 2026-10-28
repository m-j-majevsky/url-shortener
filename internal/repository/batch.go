package repository

import (
	"errors"
)

type (
	// StoreItem — элемент пакета на сохранение: (токен, URL, пользователь)
	// и признаки конфликтов (ConflictedToken, ConflictedURL).
	StoreItem struct {
		CorrelationID   string
		Token           string
		OriginalURL     string
		ConflictedToken bool
		ConflictedURL   bool
	}

	// StoreBatch — пакет на сохранение.
	StoreBatch []StoreItem

	// ToMarkDeletedReqItem — запрос на мягкое удаление токена от имени пользователя.
	ToMarkDeletedReqItem struct {
		Token  string
		UserID string
	}

	// ToMarkDeletedReqBatch — пакет запросов на мягкое удаление.
	ToMarkDeletedReqBatch []ToMarkDeletedReqItem

	// UserURL — запись о коротком URL пользователя.
	UserURL struct {
		ShortURL    string `db:"token"`
		OriginalURL string `db:"original_url"`
	}

	// UserURLs — список записей о коротких URL пользователя.
	UserURLs []UserURL
)

// Методы и утилиты для работы с StoreBatch


// MayBeAddErrors собирает признаки конфликтов пакета (занятые токены,
// уже существующие URL) в один joined error.
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

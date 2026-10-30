// Package model описывает структуры запросов и ответов HTTP API и типы,
// которыми обмениваются слои хендлеров и сервиса.
package model

type (
	// ShortenReq — тело запроса POST /api/shorten;
	// URL — сокращаемая ссылка.
	ShortenReq struct {
		URL string `json:"url" valid:"url,required"`
	}

	// BatchShortenReqItem — элемент батча в POST /api/shorten/batch:
	// CorrelationID — идентификатор, который выбирает клиент и получает
	// обратно в ответе для сопоставления пары запрос-ответ;
	// OriginalURL — сокращаемая ссылка.
	BatchShortenReqItem struct {
		CorrelationID string `json:"correlation_id" valid:"required"`
		OriginalURL   string `json:"original_url" valid:"url,required"`
	}

	// BatchShortenReq — тело запроса POST /api/shorten/batch.
	BatchShortenReq []BatchShortenReqItem

	// TokensToMarkDeleted — тело запроса DELETE /api/user/urls:
	// JSON-массив токенов, которые пользователь помечает как удалённые.
	TokensToMarkDeleted []string
)

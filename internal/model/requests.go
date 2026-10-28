// Package model описывает структуры запросов и ответов HTTP API и типы,
// которыми обмениваются слои хендлеров и сервиса.
package model

type (
	ShortenReq struct {
		URL string `json:"url" valid:"url,required"`
	}

	BatchShortenReqItem struct {
		CorrelationID string `json:"correlation_id" valid:"required"`
		OriginalURL   string `json:"original_url" valid:"url,required"`
	}

	BatchShortenReq []BatchShortenReqItem

	TokensToMarkDeleted []string
)

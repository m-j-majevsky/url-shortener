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
)

package model

type (
	ShortenRes struct {
		Result string `json:"result"`
	}

	BatchShortenResItem struct {
		CorrelationID string `json:"correlation_id"`
		ShortURL      string `json:"short_url"`
		ConflictedURL bool   `json:"-"`
	}

	BatchShortenRes []BatchShortenResItem

	UserURLsResItem struct {
		ShortURL    string `json:"short_url" db:"token"`
		OriginalURL string `json:"original_url" db:"original_url"`
	}

	UserURLsRes []UserURLsResItem
)

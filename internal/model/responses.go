package model

type (
	// ShortenRes — тело ответа на POST /api/shorten:
	// Result — полный сокращённый URL (базовый URL сервиса плюс токен).
	ShortenRes struct {
		Result string `json:"result"`
	}

	// BatchShortenResItem — ответ на элемент батча: CorrelationID из
	// запроса и ShortURL — полный сокращённый URL. ConflictedURL —
	// внутренний признак: original_url уже был в хранилище до запроса,
	// независимо от того, каким пользователем он был сохранён; не
	// сериализуется (json "-") и используется хендлером при выборе кода
	// ответа батча (201 или 409). Конфликтному элементу возвращается
	// ранее выданный токен.
	BatchShortenResItem struct {
		CorrelationID string `json:"correlation_id"`
		ShortURL      string `json:"short_url"`
		ConflictedURL bool   `json:"-"`
	}

	// BatchShortenRes — тело ответа на POST /api/shorten/batch.
	BatchShortenRes []BatchShortenResItem

	// UserURLsResItem — элемент списка ссылок пользователя
	// (GET /api/user/urls): ShortURL — полный сокращённый URL,
	// OriginalURL — исходная ссылка.
	UserURLsResItem struct {
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
	}

	// UserURLsRes — тело ответа на GET /api/user/urls.
	UserURLsRes []UserURLsResItem
)

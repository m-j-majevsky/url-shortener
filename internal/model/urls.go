package model

import "encoding/json"

type URL string

func (lu *URL) String() string {
	return string(*lu)
}

func NewURL(value string) URL {
	return URL(value)
}

const (
	EmptyURL = URL("")
)

// Реализуем интерфейс json.Marshaler
func (u URL) MarshalJSON() ([]byte, error) {
	// Просто оборачиваем строку в кавычки — это валидный JSON для строки
	return json.Marshal(string(u))
}

// Реализуем интерфейс json.Unmarshaler
func (u *URL) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*u = URL(s)
	return nil
}

type (
	PostApiShortenReq struct {
		URL string `json:"url" valid:"url,required"`
	}

	PostApiShortenRes struct {
		Result string `json:"result"`
	}
)

package model

import "encoding/json"

type (
	PostApiShortenReq struct {
		URL string `json:"url" valid:"url,required"`
	}

	PostApiShortenRes struct {
		Result string `json:"result"`
	}
)

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

package model

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

type (
	PostApiShortenReq struct {
		URL string `json:"url" valid:"url,required"`
	}

	PostApiShortenRes struct {
		Result string `json:"result"`
	}
)

package model

type LongURL string

func (lu *LongURL) String() string {
	return string(*lu)
}

func NewLongURL(value string) LongURL {
	return LongURL(value)
}

type ShortURL string

func (su *ShortURL) String() string {
	return string(*su)
}

func NewShortURL(value string) ShortURL {
	return ShortURL(value)
}

const (
	EmptyShortURL = ShortURL("")
	EmptyLongURL  = LongURL("")
)

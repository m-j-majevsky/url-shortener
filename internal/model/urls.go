package model

type LongURL string
type ShortURL string

const (
	EmptyShortURL = ShortURL("")
	EmptyLongURL  = LongURL("")
)

type URLStorageError struct {
	Message string
}

func (e *URLStorageError) Error() string {
	return e.Message
}

type URLStorage interface {
	Add(key ShortURL, value LongURL) *URLStorageError
	Get(key ShortURL) (LongURL, *URLStorageError)
	Find(value LongURL) (ShortURL, bool)
	Remove(key ShortURL)
}

package model

type LongURL string

type ShortURL string

type URLStorageError struct {
	Message string
}

func (e *URLStorageError) Error() string {
	return e.Message
}

type URLStorage interface {
	Add(lu LongURL) (ShortURL, *URLStorageError)
	Get(su ShortURL) (LongURL, *URLStorageError)
	Remove(su ShortURL)
}

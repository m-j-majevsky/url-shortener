package repository

import (
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type storageImpl struct {
	db map[string]string
}

var (
	storage storageImpl
)

func init() {
	storage.db = make(map[string]string)
}

func ProvideURLStorage() model.URLStorage {
	return &storage
}

func (s *storageImpl) Add(key model.ShortURL, value model.LongURL) *model.URLStorageError {
	shortURL := string(key)
	longURL := string(value)
	s.db[shortURL] = longURL

	// В этой реализации специальных ошибок model.URLStorageError
	// в случае добавления в базу не предвидится
	return nil
}

func (s *storageImpl) Get(key model.ShortURL) (model.LongURL, *model.URLStorageError) {
	shortURL := string(key)
	longURLString, ok := s.db[shortURL]
	if !ok {
		return model.EmptyLongURL, &model.URLStorageError{
			Message: "токен не найден",
		}
	}
	return model.LongURL(longURLString), nil
}

func (s *storageImpl) Find(value model.LongURL) (model.ShortURL, bool) {
	longURL := string(value)
	for token, url := range s.db {
		if url == longURL {
			return model.ShortURL(token), true
		}
	}
	return model.EmptyShortURL, false
}

func (s *storageImpl) Remove(key model.ShortURL) {
	shortURL := string(key)
	delete(s.db, string(shortURL))

	// В этой реализации специальных ошибок model.URLStorageError
	// в случае удаления из базы не предвидится
}

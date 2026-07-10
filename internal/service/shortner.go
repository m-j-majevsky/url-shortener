package service

import (
	"fmt"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type storageImpl struct {
	db map[string]string
}

const (
	tokenLength = 8
)

var (
	storage storageImpl
	mu      sync.Mutex
)

func init() {
	storage.db = make(map[string]string)
}

func ProvideURLStorage() model.URLStorage {
	return storage
}

func (s storageImpl) Add(lu model.LongURL) (model.ShortURL, *model.URLStorageError) {
	url, err := s.shortenURL(string(lu))
	return model.ShortURL(url), err
}

func (s storageImpl) Get(su model.ShortURL) (model.LongURL, *model.URLStorageError) {
	url, err := s.resolveToken(string(su))
	return model.LongURL(url), err
}

func (s storageImpl) Remove(su model.ShortURL) {
	mu.Lock()
	defer mu.Unlock()

	delete(s.db, string(su))
}

// Логика следующих двух вункций адаптирована на основе вывода Алисы AI

// Пытается сохранить длинную ссылку и вернуть короткий токен.
// Если ссылка уже есть — возвращает существующий токен.
func (s storageImpl) shortenURL(longURL string) (string, *model.URLStorageError) {
	mu.Lock()
	defer mu.Unlock()

	// Если такая длинная ссылка уже сокращена — возвращаем старый токен
	for token, url := range s.db {
		if url == longURL {
			return token, nil
		}
	}

	var token string
	attempts := 0
	maxAttempts := 1000 // защита от бесконечного цикла при почти заполненной БД

	for {
		token = base62.GenerateToken(tokenLength)
		if _, exists := s.db[token]; !exists {
			break
		}
		attempts++
		if attempts >= maxAttempts {
			message := fmt.Sprintf("не удалось сгенерировать уникальный токен после %d попыток", maxAttempts)
			return "", &model.URLStorageError{
				Message: message,
			}
		}
	}

	s.db[token] = longURL
	return token, nil
}

// Возвращает длинный URL по токену или ошибку, если не найден.
func (s storageImpl) resolveToken(token string) (string, *model.URLStorageError) {
	mu.Lock()
	defer mu.Unlock()

	longURL, ok := s.db[token]
	if !ok {
		return "", &model.URLStorageError{
			Message: "токен не найден",
		}
	}
	return longURL, nil
}

package service

import (
	"fmt"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

const (
	TokenLength = 8
)

var (
	mu sync.Mutex
)

// Логика следующих двух функций адаптирована на основе вывода Алисы AI

// Пытается сохранить длинную ссылку и вернуть короткий токен.
// Если ссылка уже есть — возвращает существующий токен.
func ShortenURL(s model.URLStorage, longURL model.LongURL) (model.ShortURL, *model.URLStorageError) {
	// Пока на всякий случай синхронизируем доступ к хранилищу примитивом
	mu.Lock()
	defer mu.Unlock()

	// Если такая длинная ссылка уже сокращена — возвращаем старый токен
	if shortURL, found := s.Find(longURL); found {
		return shortURL, nil
	}

	var token string
	attempts := 0
	maxAttempts := 1000 // защита от бесконечного цикла при почти заполненной БД
	for {
		token = base62.GenerateToken(TokenLength)
		if _, err := s.Get(model.ShortURL(token)); err != nil {
			// такого токена в базе нет, всё в порядке
			break
		}
		attempts++
		if attempts >= maxAttempts {
			message := fmt.Sprintf("не удалось сгенерировать уникальный токен после %d попыток", maxAttempts)
			return model.EmptyShortURL, &model.URLStorageError{
				Message: message,
			}
		}
	}

	shortURL := model.ShortURL(token)
	s.Add(shortURL, longURL)
	return shortURL, nil
}

// Возвращает длинный URL по короткому или ошибку, если не найден.
func ResolveShortURL(s model.URLStorage, shortURL model.ShortURL) (model.LongURL, *model.URLStorageError) {
	mu.Lock()
	defer mu.Unlock()

	longURL, err := s.Get(shortURL)
	if err != nil {
		return model.EmptyLongURL, err
	}
	return longURL, nil
}

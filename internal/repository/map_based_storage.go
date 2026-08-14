package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type TokenToURL map[string]model.URL

func newTokenToURL() TokenToURL {
	return make(map[string]model.URL)
}

type LocalStorage struct {
	mu sync.Mutex

	data TokenToURL
}

func NewLocalStorage() *LocalStorage {
	return &LocalStorage{
		data: newTokenToURL(),
	}
}

func (s *LocalStorage) exportRepr() storageRepr {
	result := make(storageRepr, len(s.data))
	var idx int = 1
	for su, ou := range s.data {
		result[idx-1] = itemRepr{UUID: strconv.Itoa(idx), ShortURL: su, OriginalURL: ou}
		idx += 1
	}
	return result
}

// Сохраняет текущее состояние Storage в JSON-файл.
func (s *LocalStorage) SaveToFile(path string) error {
	// Блокировка нужна, чтобы не читать частично изменённые данные из data.
	s.mu.Lock()
	defer s.mu.Unlock()

	repr := s.exportRepr()

	data, err := json.Marshal(repr)
	if err != nil {
		return fmt.Errorf("ошибка маршалинга данных из хранилища: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("ошибка записи в файл состояния хранилища: %w", err)
	}
	return nil
}

func (s *LocalStorage) importRepr(repr storageRepr) {
	s.data = make(TokenToURL, len(repr))
	for _, item := range repr {
		s.data[item.ShortURL] = item.OriginalURL
	}
}

// Загружает данные из JSON-файла и заменяет ими текущее содержимое data.
// Важно: эта функция перезаписывает мапу, а не мёржит её.
func (s *LocalStorage) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ошибка чтения файла состояния хранилища: %w", err)
	}

	var loadedRepr storageRepr
	if err := json.Unmarshal(data, &loadedRepr); err != nil {
		return fmt.Errorf("ошибка декодирования содержимого файла состояния хранилища: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.importRepr(loadedRepr)
	return nil
}

// Сохраняет longURL под токеном. Возвращает ErrTokensTaken, если токен занят.
func (s *LocalStorage) Store(_ context.Context, token string, longURL model.URL) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data[token]; exists {
		return NewErrTokensTaken([]string{token})
	}
	s.data[token] = longURL
	return nil
}

func (s *LocalStorage) Resolve(_ context.Context, token string) (model.URL, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.data[token]
	if !ok {
		return model.EmptyURL, NewErrTokenNotFound(token)
	}
	return url, nil
}

func (s *LocalStorage) BatchStore(_ context.Context, batch Batch) (Batch, error) {
	result := make(Batch, len(batch))
	copy(result, batch)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Проходимся по батчу
	for i := range result {
		token := result[i].Token
		if _, exists := s.data[token]; exists {
			// Конфликтные токены помечаем
			result[i].ConflictedToken = true
		} else {
			// Неконфликтные записи сохраняем
			s.data[token] = result[i].OriginalURL
		}
	}

	return MayBeAddErrTokenTaken(result)
}

func (s *LocalStorage) DeleteByTokens(_ context.Context, tokens []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, token := range tokens {
		delete(s.data, token)
	}

	return nil
}

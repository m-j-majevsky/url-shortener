package repository

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type TokenToURL map[string]model.URL

func NewTokenToURL() TokenToURL {
	return make(map[string]model.URL)
}

type Storage struct {
	mu sync.Mutex

	data TokenToURL
}

func NewStorage() *Storage {
	return &Storage{
		data: NewTokenToURL(),
	}
}

// Сохраняет текущее состояние Storage в JSON-файл.
func (s *Storage) SaveToFile(path string) error {
	// Блокировка нужна, чтобы не читать частично изменённые данные из data.
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("ошибка маршалинга данных из хранилища: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("ошибка записи в файл состояния хранилища: %w", err)
	}
	return nil
}

// Загружает данные из JSON-файла и заменяет ими текущее содержимое data.
// Важно: эта функция перезаписывает мапу, а не мёржит её.
func (s *Storage) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ошибка чтения файла состояния хранилища: %w", err)
	}

	var loadedData TokenToURL
	if err := json.Unmarshal(data, &loadedData); err != nil {
		return fmt.Errorf("ошибка декодирования содержимого файла состояния хранилища: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = loadedData
	return nil
}

// Ошибка, когда токен уже занят
type ErrTokenTaken struct {
	Token string
}

func NewErrTokenTaken(tok string) *ErrTokenTaken {
	return &ErrTokenTaken{
		Token: tok,
	}
}

func (e *ErrTokenTaken) Error() string {
	return fmt.Sprintf("токен %q занят", e.Token)
}

// Сохраняет longURL под токеном. Возвращает ErrTokenTaken, если токен занят.
func (s *Storage) Store(token string, longURL model.URL) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data[token]; exists {
		return NewErrTokenTaken(token)
	}
	s.data[token] = longURL
	return nil
}

func (s *Storage) Resolve(token string) (model.URL, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.data[token]
	if !ok {
		return model.EmptyURL, false
	}
	return url, true
}

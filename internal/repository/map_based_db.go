package repository

import (
	"fmt"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type Storage struct {
	mu sync.Mutex

	store map[string]model.URL
}

func NewStorage() *Storage {
	return &Storage{
		store: make(map[string]model.URL),
	}
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

	if _, exists := s.store[token]; exists {
		return NewErrTokenTaken(token)
	}
	s.store[token] = longURL
	return nil
}

func (s *Storage) Resolve(token string) (model.URL, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.store[token]
	if !ok {
		return model.EmptyURL, false
	}
	return url, true
}

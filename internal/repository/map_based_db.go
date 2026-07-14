package repository

import (
	"errors"
	"sync"

	"github.com/m-j-majevsky/url-shortener/internal/base62"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type storageImpl struct {
	mu    sync.Mutex
	count int64

	// Пока не переехали в Postgres будем хранить значения в памяти
	store map[string]string
}

var (
	ErrTokenNotFound  = errors.New("repository: токен не найден")
	ErrURLShortenning = errors.New("repository: ошибка кодирования длинного URL")
)

type URLStorage interface {
	ShortenAndStore(value model.LongURL) (model.ShortURL, error)
	Resolve(key model.ShortURL) (model.LongURL, bool)
	find(value model.LongURL) (model.ShortURL, bool)
}

func NewURLStorage(startCount int64) URLStorage {
	return &storageImpl{
		count: startCount,
		store: make(map[string]string),
	}
}

func (s *storageImpl) ShortenAndStore(value model.LongURL) (model.ShortURL, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.count
	token, err := base62.EncodeInt64ToBase62Fixed(id)
	if err != nil {
		return model.EmptyShortURL, errors.Join(ErrURLShortenning, err)
	}

	s.store[token] = value.String()
	s.count++

	return model.NewShortURL(token), nil
}

func (s *storageImpl) Resolve(key model.ShortURL) (model.LongURL, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	val, ok := s.store[key.String()]

	return model.NewLongURL(val), ok
}

// Это дорогая реализация, т.к. потенциально лочит базу на время полного перебора её содержимого,
// но посколько в будущем предстоит переезд в Postgres, заморачиваться на собственную реализацию
// более аккуратного подхода, например, с индексом LongURL -> Short URL пока не стану
//
// С другой стороны явного требования выдавать те же самые сокращения на ранее запрошенные URL'ы нет,
// и пока логика используется только для тестового сценария
func (s *storageImpl) find(value model.LongURL) (model.ShortURL, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := value.String()
	for token, url := range s.store {
		if url == val {
			return model.NewShortURL(token), true
		}
	}
	return model.EmptyShortURL, false
}

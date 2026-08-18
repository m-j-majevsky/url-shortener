package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

type (
	TokenToURL map[string]string

	LocalStorage struct {
		mu sync.Mutex

		data TokenToURL
	}

	itemRepr struct {
		UUID        string `json:"uuid"`
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
	}

	storageRepr []itemRepr
)

func newTokenToURL() TokenToURL {
	return make(map[string]string)
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

// Сохраняет longURL под токеном. Возвращает ErrTokenTaken, если токен занят.
func (s *LocalStorage) Store(_ context.Context, token string, longURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Для проверки нарушения уникальности longURL ограничусь перебором сохраненных значений,
	// чтобы не возиться с введение дополнительного индекса (мыпы longURL -> token)
	// или другого более эффективного механизма
	for tok, url := range s.data {
		if url == longURL {
			return NewErrOriginalURLExists(tok, string(url))
		}
	}

	if _, exists := s.data[token]; exists {
		return NewErrTokenTaken(token)
	}

	s.data[token] = longURL
	return nil
}

func (s *LocalStorage) Resolve(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.data[token]
	if !ok {
		return "", NewErrTokenNotFound(token)
	}
	return url, nil
}

// Важно:
// Гарантировать уникальность Batch.Token среди элемемнов параметра batch,
// это ответсвенность вызывающего кода!
func (s *LocalStorage) BatchStore(_ context.Context, batch Batch) (Batch, error) {
	result := make(Batch, len(batch))
	copy(result, batch)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Проходимся по батчу
	for i := range result {
		resIt := &result[i]
		token := resIt.Token
		url := resIt.OriginalURL

		for t, u := range s.data {
			if u == url {
				// Конфликтный URL помечаем
				resIt.ConflictedURL = true
				// Соответствующий ему выданный ранее токен сохраняем
				resIt.Token = t
			}
		}

		if resIt.ConflictedURL {
			// Важно:
			// При наличии конфликта по исходному URL,
			// конфликт по токену не проверяем, он уже не имеет значения,
			// но и данные при конфликте по URL записываться не должны.
			continue
		}

		if _, exists := s.data[token]; exists {
			// Конфликтный токен помечаем
			resIt.ConflictedToken = true
		} else {
			// Запись resIt чиста, можно сохранять
			s.data[token] = url
		}
	}

	return MayBeAddErrors(result)
}

func (s *LocalStorage) DeleteByTokens(_ context.Context, tokens []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, token := range tokens {
		delete(s.data, token)
	}

	return nil
}

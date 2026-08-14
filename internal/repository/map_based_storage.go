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

// Сохраняет longURL под токеном. Возвращает ErrTokenTaken, если токен занят.
func (s *LocalStorage) Store(ctx context.Context, token string, longURL model.URL) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("сохранение токена прервано из-за отмены контекста: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data[token]; exists {
		return NewErrTokenTaken(token)
	}
	s.data[token] = longURL
	return nil
}

func (s *LocalStorage) Resolve(ctx context.Context, token string) (model.URL, error) {
	if err := ctx.Err(); err != nil {
		return model.EmptyURL, fmt.Errorf("поиск токена прерван из-за отмены контекста: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.data[token]
	if !ok {
		return model.EmptyURL, NewErrTokenNotFound(token)
	}
	return url, nil
}

func (s *LocalStorage) BatchStore(ctx context.Context, batch Batch) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("сохранение пакета отменено: %w", err)
	}

	// Смоделируем транзакционность обработки пакета следующим образом:
	//
	// 1. Лок на хранилище на всё время выполнения метода.
	// 2. Сперва проверяем весь батч на наличие конфликтного токена хотя бы в одном элементе.
	//    При наличии конфликта в хранилище не попадет ничего из батча.
	//    Возвращается ошибка ErrTokenTaken.
	// 3. Если конфликтов нет, сохраняем весь батч.

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, item := range batch {
		token := item.Token
		if _, exists := s.data[token]; exists {
			return NewErrTokenTaken(token)
		}
	}

	for _, item := range batch {
		s.data[item.Token] = item.OriginalURL
	}

	return nil
}

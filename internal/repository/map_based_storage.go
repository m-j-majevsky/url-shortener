package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/google/uuid"
	"github.com/m-j-majevsky/url-shortener/internal/model"
)

type (
	TokenToURL map[string]string

	UserToTokens map[string][]string

	LocalStorage struct {
		mu sync.Mutex

		data  TokenToURL
		users UserToTokens
	}

	itemRepr struct {
		UUID        string `json:"uuid"`
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
		UserID      string `json:"userID"`
	}

	storageRepr []itemRepr
)

const (
	isDeleted = ""
)

func newTokenToURL() TokenToURL {
	return make(map[string]string)
}

func newUserToTokens() UserToTokens {
	return make(map[string][]string)
}

func NewLocalStorage() *LocalStorage {
	return &LocalStorage{
		data:  newTokenToURL(),
		users: newUserToTokens(),
	}
}

func (s *LocalStorage) exportRepr() storageRepr {
	result := make(storageRepr, len(s.data))
	idx := 1
	for su, ou := range s.data {
		uid := s.findUserByToken(su)
		result[idx-1] = itemRepr{
			UUID:        strconv.Itoa(idx),
			ShortURL:    su,
			OriginalURL: ou,
			UserID:      uid,
		}
		idx += 1
	}
	return result
}

func (s *LocalStorage) findUserByToken(tok string) string {
	for user, tokens := range s.users {
		for _, t := range tokens {
			if t == tok {
				return user
			}
		}
	}
	return ""
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
		s.addTokenToUser(item.ShortURL, item.UserID)
	}
}

func (s *LocalStorage) addTokenToUser(tok, uid string) {
	// Вызывается в контексте уже захваченного s.mu
	// или при загрузке данных из файла, когда захват не имеет смысла

	// Проверяем, существует ли ключ в мапе
	if _, exists := s.users[uid]; !exists {
		// Если ключа нет, создаем новый слайс
		s.users[uid] = []string{tok}
		return
	}

	// Если ключ существует, проверяем, нет ли уже такого значения
	for _, v := range s.users[uid] {
		if v == tok {
			return // Значение уже есть, ничего не делаем
		}
	}

	// Добавляем новое значение в конец слайса
	s.users[uid] = append(s.users[uid], tok)
}

func (s *LocalStorage) deleteTokenFromUsers(token string) {
	// Вызывается в контексте уже захваченного s.mu

	for user := range s.users {
		s.deleteUsersToken(user, token)
	}
}

func (s *LocalStorage) deleteUsersToken(userID, tok string) {
	// Вызывается в контексте уже захваченного s.mu

	// Ищем индекс значения в слайсе
	ts := s.users[userID]
	for i, t := range ts {
		if t == tok {
			// Сдвигаем элементы влево
			copy(ts[i:], ts[i+1:])
			// Обрезаем слайс
			s.users[userID] = ts[:len(ts)-1]
			// Если слайс стал пустым, оставляем его для дальнейшего
			return
		}
	}
}

func (s *LocalStorage) isTokenBelongsToUser(tok, userID string) bool {
	// Вызывается в контексте уже захваченного s.mu

	toks, userExists := s.users[userID]
	if !userExists {
		return false
	}

	for _, t := range toks {
		if t == tok {
			return true
		}
	}

	return false
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
// Вызывающий код должен гарантировать отсутствие пустых token или longURL.
func (s *LocalStorage) Store(_ context.Context, token, longURL, userID string) error {
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
	s.addTokenToUser(token, userID)

	return nil
}

func (s *LocalStorage) Resolve(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	url, ok := s.data[token]
	if !ok {
		return "", NewErrTokenNotFound(token)
	}
	if url == isDeleted {
		return "", NewErrTokenIsDeleted(token)
	}
	return url, nil
}

// Важно:
// Гарантировать уникальность StoreBatch.Token среди элемемнов параметра batch,
// а также гарантировать остутствие пустых StoreBatch.Token или StoreBatch.OriginalURL,
// это ответсвенность вызывающего кода!
func (s *LocalStorage) BatchStore(_ context.Context, batch StoreBatch, userID string) (StoreBatch, error) {
	result := make(StoreBatch, len(batch))
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
			s.addTokenToUser(token, userID)
		}
	}

	return MayBeAddErrors(result)
}

func (s *LocalStorage) DeleteByTokens(_ context.Context, tokens []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, token := range tokens {
		delete(s.data, token)
		s.deleteTokenFromUsers(token)
	}

	return nil
}

func (s *LocalStorage) CheckUserExists(_ context.Context, uid string) (exists bool, err error) {
	if uid == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	exists = (s.users[uid] != nil)
	return
}

func (s *LocalStorage) CreateUser(_ context.Context) (uid string, err error) {
	uid = uuid.New().String()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[uid] = make([]string, 0)

	return
}

func (s *LocalStorage) ListUserURLs(_ context.Context, userID string) (model.UserURLsRes, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens, exists := s.users[userID]
	if !exists {
		return model.UserURLsRes{}, nil
	}

	var res model.UserURLsRes
	for _, tok := range tokens {
		if url, exists := s.data[tok]; exists && url != isDeleted {
			res = append(res, model.UserURLsResItem{
				ShortURL:    tok,
				OriginalURL: url,
			})
		}
	}

	return res, nil
}

func (s *LocalStorage) MarkUserURLsDeleted(_ context.Context, batch ToMarkDeletedReqBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range batch {
		user, token := it.UserID, it.Token

		url, foundURL := s.data[token]
		if !foundURL || url == isDeleted {
			continue
		}

		if s.isTokenBelongsToUser(token, user) {
			s.deleteUsersToken(user, token)
			s.data[token] = isDeleted
		}
	}
	return nil
}

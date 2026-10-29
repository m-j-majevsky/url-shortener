package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/google/uuid"
)

type (
	// TokenToURL — отображение токена в исходный URL.
	TokenToURL map[string]string

	// URLToToken — индекс original_url -> token: O(1)-проверка уникальности URL.
	URLToToken map[string]string

	// UserToTokens — отображение пользователя в список его токенов.
	UserToTokens map[string][]string

	// DeletedTokens — множество токенов, помеченных удалёнными.
	// Аналог признака is_deleted в PG: URL записи при удалении сохраняется,
	// повторное сохранение того же URL даёт конфликт со старым токеном.
	DeletedTokens map[string]bool

	// LocalStorage — хранилище в памяти с сохранением состояния в файл.
	//
	// Инвариант согласованности: для каждой записи data[token] == url
	// существует byURL[url] == token, и наоборот — отображение биективно,
	// потому что Store и BatchStore не записывают дубликаты URL.
	// Все точки изменения data обязаны синхронно менять byURL:
	// Store, BatchStore, DeleteByTokens, importRepr.
	// Мягкое удаление (MarkUserURLsDeleted) обе мапы не меняет.
	LocalStorage struct {
		mu sync.Mutex

		data    TokenToURL
		byURL   URLToToken // индекс original_url -> token
		users   UserToTokens
		deleted DeletedTokens
	}

	itemRepr struct {
		UUID        string `json:"uuid"`
		ShortURL    string `json:"short_url"`
		OriginalURL string `json:"original_url"`
		UserID      string `json:"userID"`
		Deleted     bool   `json:"deleted,omitempty"`
	}

	storageRepr []itemRepr
)

// newTokenToURL создаёт пустую карту токен -> URL.
func newTokenToURL() TokenToURL {
	return make(map[string]string)
}

// newURLToToken создаёт пустой индекс original_url -> token.
func newURLToToken() URLToToken {
	return make(map[string]string)
}

// newUserToTokens создаёт пустую карту пользователь -> токены.
func newUserToTokens() UserToTokens {
	return make(map[string][]string)
}

// NewLocalStorage создаёт пустое локальное хранилище.
func NewLocalStorage() *LocalStorage {
	return &LocalStorage{
		data:    newTokenToURL(),
		byURL:   newURLToToken(),
		users:   newUserToTokens(),
		deleted: make(DeletedTokens),
	}
}

// exportRepr сериализует состояние хранилища для записи в файл.
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
			Deleted:     s.deleted[su],
		}
		idx += 1
	}
	return result
}

// findUserByToken ищет пользователя, владеющего токеном.
// Стоимость — O(n) по числу всех записей на каждый вызов; вызывается
// из exportRepr в цикле, итого O(n²) на сохранение состояния.
// Осознанный компромисс локального режима: FILE_STORAGE_PATH
// рассчитан на небольшие объёмы данных.
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

// importRepr перезаписывает состояние данными из файла; вызывается под s.mu.
func (s *LocalStorage) importRepr(repr storageRepr) {
	s.data = make(TokenToURL, len(repr))
	s.byURL = make(URLToToken, len(repr))
	// Перезапись состояния сбрасывает признаки удаления (аналог is_deleted)
	s.deleted = make(DeletedTokens)
	for _, item := range repr {
		// Пустой original_url означал признак удаления.
		if item.OriginalURL == "" {
			s.deleted[item.ShortURL] = true
			continue
		}
		s.data[item.ShortURL] = item.OriginalURL
		s.byURL[item.OriginalURL] = item.ShortURL
		s.addTokenToUser(item.ShortURL, item.UserID)
		if item.Deleted {
			s.deleted[item.ShortURL] = true
		}
	}
}

// addTokenToUser добавляет токен в список пользователя.
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

// deleteTokenFromUsers убирает токен из списков всех пользователей.
func (s *LocalStorage) deleteTokenFromUsers(token string) {
	// Вызывается в контексте уже захваченного s.mu

	for user := range s.users {
		s.deleteUsersToken(user, token)
	}
}

// deleteUsersToken убирает токен из списка конкретного пользователя.
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

// isTokenBelongsToUser проверяет принадлежность токена пользователю.
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

	// Уникальность original_url проверяется по индексу за O(1);
	// раньше здесь был полный перебор значений data (см. журнал, кандидат № 1).
	if tok, ok := s.byURL[longURL]; ok {
		return NewErrOriginalURLExists(tok, longURL)
	}

	if _, exists := s.data[token]; exists {
		return NewErrTokenTaken(token)
	}

	s.data[token] = longURL
	s.byURL[longURL] = token
	s.addTokenToUser(token, userID)

	return nil
}

// Resolve возвращает URL по токену: удалённый — ErrTokenIsDeleted,
// несуществующий — ErrTokenNotFound.
func (s *LocalStorage) Resolve(_ context.Context, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Признак удаления проверяется раньше поиска:
	// удалённый токен — 410,
	// несуществующий — 404 (как в PG)
	if s.deleted[token] {
		return "", NewErrTokenIsDeleted(token)
	}

	url, ok := s.data[token]
	if !ok {
		return "", NewErrTokenNotFound(token)
	}
	return url, nil
}

// Важно:
// Гарантировать уникальность StoreBatch.Token среди элементов параметра batch,
// а также гарантировать отсутствие пустых StoreBatch.Token или StoreBatch.OriginalURL,
// это ответственность вызывающего кода!
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

		if t, ok := s.byURL[url]; ok {
			// Конфликтный URL помечаем, соответствующий ему
			// выданный ранее токен сохраняем
			resIt.ConflictedURL = true
			resIt.Token = t
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
			s.byURL[url] = token
			s.addTokenToUser(token, userID)
		}
	}

	return MayBeAddErrors(result)
}

// DeleteByTokens жёстко удаляет записи по токенам.
func (s *LocalStorage) DeleteByTokens(_ context.Context, tokens []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, token := range tokens {
		if url, ok := s.data[token]; ok {
			delete(s.byURL, url)
		}
		delete(s.data, token)
		// Жёсткое удаление снимает и признак мягкого: после него токен
		// отвечает 404, а не 410 — в PG строка снесена целиком
		delete(s.deleted, token)
		s.deleteTokenFromUsers(token)
	}

	return nil
}

// CheckUserExists проверяет существование пользователя по ID.
func (s *LocalStorage) CheckUserExists(_ context.Context, uid string) (exists bool, err error) {
	if uid == "" {
		return false, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	exists = (s.users[uid] != nil)
	return exists, nil
}

// CreateUser регистрирует пользователя с UUID и возвращает его ID.
func (s *LocalStorage) CreateUser(_ context.Context) (uid string, err error) {
	uid = uuid.New().String()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.users[uid] = make([]string, 0)

	return uid, nil
}

// ListUserURLs возвращает не удалённые URL пользователя.
func (s *LocalStorage) ListUserURLs(_ context.Context, userID string) (UserURLs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokens, exists := s.users[userID]
	if !exists {
		return UserURLs{}, nil
	}

	var res UserURLs
	for _, tok := range tokens {
		if url, exists := s.data[tok]; exists && !s.deleted[tok] {
			res = append(res, UserURL{
				ShortURL:    tok,
				OriginalURL: url,
			})
		}
	}

	return res, nil
}

// MarkUserURLsDeleted помечает токены пользователя удалёнными.
func (s *LocalStorage) MarkUserURLsDeleted(_ context.Context, batch ToMarkDeletedReqBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range batch {
		user, token := it.UserID, it.Token

		if _, found := s.data[token]; !found {
			continue
		}

		if s.isTokenBelongsToUser(token, user) {
			// Мягкое удаление — как в PG: URL записи сохраняется, признак
			// удаления хранится отдельно, владелец и список токенов не меняются
			s.deleted[token] = true
		}
	}
	return nil
}

// Ping проверяет доступность хранилища.
// Хранилище целиком в памяти и всегда доступно.
func (s *LocalStorage) Ping(_ context.Context) error {
	return nil
}

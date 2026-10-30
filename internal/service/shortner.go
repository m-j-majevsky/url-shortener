// Package service реализует бизнес-логику сокращения URL поверх хранилища:
// генерацию токенов, разрешение конфликтов, пакетные операции
// и асинхронный процессор удаления токенов.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/m-j-majevsky/url-shortener/internal/crypto"
	"github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

// ---------------------------------------------------------------------------
// Интерфейсы хранилища.
// Реализация — repository.LocalStorage и repository.pgStorage,
// удовлетворяющие всем интерфейсам неявно.
// ---------------------------------------------------------------------------

// URLStore — сохранение одиночной записи и обратное преобразование токена.
type URLStore interface {
	// Store сохраняет пару (токен, URL) для пользователя userID.
	// Возвращает ErrOriginalURLExists, если исходный URL уже сохранён
	// (в ошибке указан существующий токен),
	// или ErrTokenTaken, если токен занят другим URL.
	Store(ctx context.Context, token, longURL, userID string) error

	// Resolve возвращает исходный URL по токену.
	// Возвращает ErrTokenNotFound, если токен не найден,
	// или ErrTokenIsDeleted, если токен уже удалён пользователем.
	Resolve(ctx context.Context, token string) (string, error)
}

// BatchURLStore — пакетное сохранение записей.
type BatchURLStore interface {
	// BatchStore сохраняет батч пар (токен, URL) для пользователя userID.
	// Реализует частичный успех: возвращает батч, дополненный признаками
	// конфликтов (ConflictedURL) по элементам, которые не удалось сохранить.
	// Уникальность StoreBatch.Token среди элементов батча и отсутствие
	// пустых значений гарантирует вызывающий код.
	BatchStore(ctx context.Context, batch repository.StoreBatch, userID string) (repository.StoreBatch, error)
}

// UserURLsStore — операции пользователя со своими записями: листинг и постановка на удаление.
type UserURLsStore interface {
	// ListUserURLs возвращает все записи (короткий URL, исходный URL)
	// пользователя userID за время его жизни.
	// Сервис мапит записи хранилища в DTO ответа API.
	ListUserURLs(ctx context.Context, userID string) (repository.UserURLs, error)

	// MarkUserURLsDeleted помечает токены из батча как удалённые в хранилище.
	MarkUserURLsDeleted(ctx context.Context, batch repository.ToMarkDeletedReqBatch) error
}

// UserStore — регистрация и проверка пользователя.
type UserStore interface {
	// CheckUserExists проверяет, существует ли пользователь с идентификатором uid.
	CheckUserExists(ctx context.Context, uid string) (bool, error)

	// CreateUser регистрирует нового пользователя и возвращает его идентификатор.
	CreateUser(ctx context.Context) (string, error)
}

// StorageCleaner — зачистка записей по списку токенов.
type StorageCleaner interface {
	// DeleteByTokens удаляет из хранилища записи с токенами tokens.
	DeleteByTokens(ctx context.Context, tokens []string) error
}

// StoragePinger — проверка доступности хранилища.
// Входит в композит Storage: метод Ping есть у обеих реализаций.
type StoragePinger interface {
	// Ping проверяет доступность хранилища.
	Ping(ctx context.Context) error
}

// Storage — композиция всех интерфейсов хранилища, используемых сервисом.
// Используется в ShortenerConfig для передачи хранилища в сервис.
type Storage interface {
	URLStore
	BatchURLStore
	UserURLsStore
	UserStore
	StorageCleaner
	StoragePinger
}

// ShortenerConfig — конфигурация сервиса. Собирается DefaultShortenerConfig
// с последующей установкой хранилища; валидируется NewShortener.
type ShortenerConfig struct {
	Storage Storage

	RandProv crypto.RandomByteProvider // источник случайных данных для генератора токенов

	MinTokenLength        int // минимальная длина токена в символах Base62
	MaxTokenLength        int // максимальная длина токена
	BytesToGenerate       int // сколько случайных байт брать на одну попытку
	MaxGeneratingAttempts int // максимальное число попыток сгенерировать подходящий токен

	MaxStoringAttempts int // максимальное число попыток сохранить (токен, URL) в хранилище

	DeletionQueueBuffer       int           // глубина канала для накопления записей на удаление
	DeletionBatchSize         int           // размер батча для асинхронного удаления токенов
	DeletionQueueFlushTimeout time.Duration // период сбрасывания запросов из канала на обработку в хранилище

	DeadLetterLogFile string // имя файла, в который сохраняются пары (токен, юзер) не обработанные при запросе DELETE /api/user/urls

	Logger *zap.Logger // логгер; если nil — используется no-op
}

// DefaultShortenerConfig возвращает конфигурацию со значениями по умолчанию.
// Storage не устанавливается — его задаёт вызывающий код.
func DefaultShortenerConfig() ShortenerConfig {
	return ShortenerConfig{
		// Storage не устанавливается!
		RandProv:                  crypto.CryptoRandProvider{},
		MinTokenLength:            6,
		MaxTokenLength:            10,
		BytesToGenerate:           6,
		MaxGeneratingAttempts:     10,
		MaxStoringAttempts:        10,
		DeletionQueueBuffer:       1024,
		DeletionBatchSize:         1024,
		DeletionQueueFlushTimeout: 10 * time.Second,
	}
}

// Shortener — реализация сервисного слоя: фасад над хранилищем
// с генерацией токенов и асинхронной очередью удалений.
type Shortener struct {
	config                 ShortenerConfig
	deletionQueue          chan repository.ToMarkDeletedReqItem
	deletionQueueMutex     sync.RWMutex
	deletionQueueClosed    bool
	deadLetterLogFile      *os.File
	deadLetterLogFileMutex sync.Mutex

	log *zap.Logger // логгер для сервиса
}

const errCfgHeader = "ошибка конфигурации сервиса"

// NewShortener валидирует конфигурацию и создаёт сервис. Возвращает ошибку,
// если обязательные поля не заданы или числовые границы не положительны.
func NewShortener(cfg ShortenerConfig) (*Shortener, error) {
	if cfg.Storage == nil {
		return nil, fmt.Errorf("%s: не задано хранилище", errCfgHeader)
	}
	if cfg.RandProv == nil {
		return nil, fmt.Errorf("%s: не задан генератор случайных данных", errCfgHeader)
	}
	if cfg.BytesToGenerate <= 0 {
		return nil, fmt.Errorf("%s: число генерируемых байт должно быть положительным", errCfgHeader)
	}
	if cfg.MinTokenLength <= 0 || cfg.MaxTokenLength <= 0 {
		return nil, fmt.Errorf("%s: границы длин токена должны быть положительными", errCfgHeader)
	}
	if cfg.MinTokenLength > cfg.MaxTokenLength {
		return nil, fmt.Errorf("%s: наименьшая допустимая длина токена не должна превосходить наибольшую", errCfgHeader)
	}
	if cfg.MaxGeneratingAttempts <= 0 {
		return nil, fmt.Errorf("%s: количество попыток создать уникальный токен должно быть положительным", errCfgHeader)
	}
	if cfg.MaxStoringAttempts <= 0 {
		return nil, fmt.Errorf("%s: количество попыток сделать запись в хранилище должно быть положительным", errCfgHeader)
	}
	if cfg.DeletionBatchSize <= 0 {
		return nil, fmt.Errorf("%s: размер батча удаляемых за раз записей должен быть положительным", errCfgHeader)
	}
	if cfg.DeletionQueueBuffer <= 0 {
		return nil, fmt.Errorf("%s: размер очереди запросов на удаление должен быть положительным", errCfgHeader)
	}

	log := cfg.Logger
	if log == nil {
		log = zap.NewNop()
	}

	shortener := &Shortener{
		config:        cfg,
		deletionQueue: make(chan repository.ToMarkDeletedReqItem, cfg.DeletionQueueBuffer),
		log:           log,
	}

	return shortener, nil
}

// StopItemsDeletionProcessor закрывает очередь запросов на удаление.
// Повторный вызов безопасен.
//
// Закрытие сериализовано с putInDeletionQueue через RWMutex:
// отправки в очередь под read-lock завершаются раньше закрытия,
// поэтому паника «send on closed channel» невозможна даже в сценарии,
// когда хендлеры работают на момент останова сервиса.
func (s *Shortener) StopItemsDeletionProcessor() {
	s.deletionQueueMutex.Lock()
	defer s.deletionQueueMutex.Unlock()

	if !s.deletionQueueClosed {
		s.deletionQueueClosed = true
		close(s.deletionQueue)
	}
}

// CleanUp закрывает dead-letter-файл и обнуляет его дескриптор.
// Повторная запись возможна: writeToDeadLetterLog переоткроет файл.
func (s *Shortener) CleanUp() {
	s.deadLetterLogFileMutex.Lock()
	defer s.deadLetterLogFileMutex.Unlock()

	if s.deadLetterLogFile != nil {
		s.deadLetterLogFile.Close()
		s.deadLetterLogFile = nil
	}
}

// generateTokens генерирует num различных base62-токенов,
// длина каждого из которых лежит в отрезке
// [config.MinTokenLength, config.MахTokenLength]
func (s *Shortener) generateTokens(num int) ([]string, error) {
	cfg := s.config
	tokens := make(map[string]struct{}, num)
	for attempt := 0; attempt < cfg.MaxGeneratingAttempts*num; attempt++ {
		bytes, err := crypto.GenerateRandomBytes(s.config.RandProv, cfg.BytesToGenerate)
		if err != nil {
			return []string{}, fmt.Errorf("ошибка генерации случайного токена: %w", err)
		}

		tok := encoding.EncodeToBase62(bytes)
		if len(tok) >= cfg.MinTokenLength && len(tok) <= cfg.MaxTokenLength {
			// Попали в допустимый диапазон длины очередного сгенерированного токена

			if _, found := tokens[tok]; found {
				// Такой токен уже есть, попытаемся еще раз
				continue
			}

			tokens[tok] = struct{}{}

			if len(tokens) == num {
				// Успешно сгенерировали num различных токенов
				break
			}
		}
	}

	if len(tokens) < num {
		return []string{}, fmt.Errorf("ошибка при генерации %d различных токенов с длиной в диапазоне [%d,%d] за %d попыток",
			num, cfg.MinTokenLength, cfg.MaxTokenLength, cfg.MaxGeneratingAttempts)
	}

	result := make([]string, 0, num)
	for tok := range tokens {
		result = append(result, tok)
	}
	return result, nil
}

// GenerateAndStore генерирует токен и сохраняет URL с ретраями по занятым токенам.
// Если URL уже сохранён, возвращает старый токен и ErrOriginalURLExists;
// прочие ошибки хранилища возвращаются вызывающему коду; при исчерпании попыток — ошибка с их числом.
func (s *Shortener) GenerateAndStore(ctx context.Context, longURL, userID string) (string, error) {
	for i := 0; i < s.config.MaxStoringAttempts; i++ {
		tokens, err := s.generateTokens(1)
		if err != nil {
			// Ошибка библиотечного генератора, контекста,
			// или не удалось выполнить ограничения на токен из конфига
			//
			// Ошибку дополнительно не оборачиваю, т.к. все обертки сделаны в GenerateAndStore
			return "", err
		}

		token := tokens[0]
		err = s.config.Storage.Store(ctx, token, longURL, userID)

		if err == nil {
			// Успех
			return token, nil
		}

		var eoue *repository.ErrOriginalURLExists
		if errors.As(err, &eoue) {
			// longURL уже был в хранилище.
			// Возвращаем относящийся к нему сохраненный ранее токен
			// и признак ошибки для обработки в раутере
			return eoue.StoredToken, NewErrOriginalURLExists(eoue.StoredToken, eoue.URL)
		}

		var ett *repository.ErrTokenTaken
		if !errors.As(err, &ett) {
			// Прочие возможные ошибки репозитория или контекста,
			// отличные от "токен занят", отдаем наверх;
			// в частности, сюда попадает repository.ErrTokenIdDeleted
			return "", fmt.Errorf("ошибка сохранения данных: %w", err)
		}
	}

	return "", fmt.Errorf("не удалось сохранить URL за %d попыток", s.config.MaxStoringAttempts)
}

// Resolve разворачивает токен в исходный URL, транслируя ошибки хранилища
// в ошибки сервиса: ErrTokenNotFound, ErrTokenIsDeleted.
func (s *Shortener) Resolve(ctx context.Context, token string) (string, error) {
	url, err := s.config.Storage.Resolve(ctx, token)
	if err != nil {
		// Транслируем ошибки хранилища в ошибки сервиса:
		// наружу протекают только типы сервиса (service.Err*).
		var etnf *repository.ErrTokenNotFound
		if errors.As(err, &etnf) {
			return "", NewErrTokenNotFound(etnf.Token)
		}
		var erid *repository.ErrTokenIsDeleted
		if errors.As(err, &erid) {
			return "", NewErrTokenIsDeleted(erid.Token)
		}
		return "", fmt.Errorf("ошибка хранилища: %w", err)
	}
	return url, nil
}

// Ping проверяет доступность хранилища.
func (s *Shortener) Ping(ctx context.Context) error {
	return s.config.Storage.Ping(ctx)
}

// CheckUserExists проверяет, существует ли пользователь с идентификатором userID.
func (s *Shortener) CheckUserExists(ctx context.Context, userID string) (bool, error) {
	found, err := s.config.Storage.CheckUserExists(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("ошибка хранилища: %w", err)
	}
	return found, nil
}

// CreateUser регистрирует нового пользователя и возвращает его идентификатор.
func (s *Shortener) CreateUser(ctx context.Context) (string, error) {
	uid, err := s.config.Storage.CreateUser(ctx)
	if err != nil {
		return "", fmt.Errorf("ошибка хранилища: %w", err)
	}
	return uid, nil
}

// BatchStore сохраняет пакет URL с ретраями занятых токенов. При исчерпании
// MaxStoringAttempts коммит откатывается (restoreWithError) и возвращается
// ошибка с числом несохранённых URL; иначе — результат с признаками ConflictedURL
// для уже существовавших записей.
func (s *Shortener) BatchStore(ctx context.Context, req model.BatchShortenReq, userID string) (model.BatchShortenRes, error) {
	reqLen := len(req)
	if reqLen == 0 {
		return model.BatchShortenRes{}, nil
	}

	// batch вводится для внутреннего представления
	// и обработки содержимого входного пакета в методах сервиса и хранилища
	batch := newStoreBatch(req)
	// В stored накапливаем успешно сохраненные в БД элементы batch
	stored := make(repository.StoreBatch, 0, reqLen)

	// Отсчет попыток сохранения до успешной или до достижения лимита попыток
	for nAttempts := 0; nAttempts < s.config.MaxStoringAttempts; nAttempts++ {
		tokens, err := s.generateTokens(len(batch))
		if err != nil {
			// Ошибки механизма генерации токенов
			// err дополнительно не оборачиваю, т.к. все обертки сделаны в generateTokens
			return model.BatchShortenRes{}, s.restoreWithError(ctx, stored, err)
		}

		assignTokensToStoreBatchItems(batch, tokens)

		storageRes, err := s.config.Storage.BatchStore(ctx, batch, userID)
		if err == nil {
			// Полный успех, можно переходить к оформлению ответа
			stored = append(stored, storageRes...)
			break
		}

		// Далее до конца блока выполняется err != nil,
		// поэтому обработка зависит от специфики ошибки

		// Собираем успешно записанные или имевшие конфликт по URL запросы
		stored = appendStoredItems(stored, storageRes)

		if len(stored) == reqLen {
			// Проблемных элементов нет; переходим к оформлению ответа
			break
		}

		var ett *repository.ErrTokenTaken
		if errors.As(err, &ett) {
			// Есть занятые токены для некоторых записей, возвращенных в storageRes;
			// для них предпримем новую попытку генерации токенов и сохранения,
			// предварительно подготовив данные на итерацию
			batch = batchForNextAttempt(storageRes)
			continue
		}

		// Прочие ошибки от БД отдаем наверх, предварительно предприняв попытку зачистить успешно закоммиченные данные
		return model.BatchShortenRes{}, s.restoreWithError(ctx, stored, fmt.Errorf("ошибка сохранения пакета: %w", err))
	} // for on nAttempts

	if len(stored) < reqLen {
		// Дошли сюда — попытки исчерпаны, а часть элементов так и не сохранена.
		// Молча возвращать неполный ответ нельзя: клиент отправил reqLen URL,
		// а получил бы меньше без единой ошибки.
		// Зачищаем закоммиченное этим запросом и отдаем ошибку наверх.
		return model.BatchShortenRes{}, s.restoreWithError(ctx, stored,
			fmt.Errorf("исчерпаны попытки сохранения пакета: не удалось сохранить %d URL из %d за %d попыток", reqLen-len(stored), reqLen, s.config.MaxStoringAttempts))
	}

	return buildBatchShortenRes(stored), nil
}

// assignTokensToStoreBatchItems распихивает по пакету req токены из tokens.
//
// Модифицирует первый аргумент req.
//
// Вызывающий код ответственен за то, чтобы в срезе tokens было
// достаточно токенов для всех элементов пакета batch
func assignTokensToStoreBatchItems(req repository.StoreBatch, tokens []string) {
	for i := range req {
		req[i].Token = tokens[i]
	}
}

// appendStoredItems пополняет коллекцию stored теми
// полученными от слоя хранилища элементами storageRes,
// которые или успешно добавлены в хранилище, или уже присутствовали в нём
func appendStoredItems(stored, storageRes repository.StoreBatch) repository.StoreBatch {
	for _, it := range storageRes {
		if it.ConflictedURL || !it.ConflictedToken {
			// добавляем в список успешно обработанных (stored)
			stored = append(stored, it)
		}
	}
	return stored
}

// batchForNextAttempt формирует на основе ответа от хранилища
// пакет запросов на сохранение для очередной попытки
// генерации токенов и сохранения данных.
//
// В формируемый пакет попадают лишь те записи, при сохранении которых
// единственной ошибкой оказался конфликт токена.
func batchForNextAttempt(storageRes repository.StoreBatch) repository.StoreBatch {
	batch := make(repository.StoreBatch, 0, len(storageRes))
	for _, it := range storageRes {
		if it.ConflictedToken && !it.ConflictedURL {
			batchItem := repository.StoreItem{
				CorrelationID: it.CorrelationID,
				OriginalURL:   it.OriginalURL,
			}
			batch = append(batch, batchItem)
		}
	}
	return batch
}

// newStoreBatch строит батч для слоя данных из запроса API:
// сервис сам мапит модель запроса в типы repository,
// repository не знает о model.
func newStoreBatch(req model.BatchShortenReq) repository.StoreBatch {
	result := make(repository.StoreBatch, len(req))
	for i := range req {
		result[i].CorrelationID = req[i].CorrelationID
		result[i].OriginalURL = req[i].OriginalURL
	}
	return result
}

// buildBatchShortenRes преобразует ответ от слоя данных к сервисному слою
func buildBatchShortenRes(batch repository.StoreBatch) model.BatchShortenRes {
	result := make(model.BatchShortenRes, 0, len(batch))
	for i := range batch {
		it := &batch[i]
		result = append(result, model.BatchShortenResItem{
			CorrelationID: it.CorrelationID,
			ShortURL:      it.Token,
			ConflictedURL: it.ConflictedURL,
		})
	}
	return result
}

// restoreWithError пробует удалить данные из батча toRollback из хранилища.
// Откатываются лишь записи, добавленные этим запросом: элемент с признаком
// ConflictedURL уже существовал в хранилище до запроса (его токен — чужой),
// и его удаление затронуло бы чужие данные — DeleteByTokens не фильтрует по пользователю.
// В случае успешного удаления возвращает errToReturn.
// В случае ошибки отката, возвращает errors.Join ошибки errToReturn и ошибки,
// не позволившей выполнить откат.
func (s *Shortener) restoreWithError(ctx context.Context, toRollback repository.StoreBatch, errToReturn error) error {
	errs := []error{errToReturn}
	rollbackBatch := make(repository.StoreBatch, 0, len(toRollback))
	for _, it := range toRollback {
		if !it.ConflictedURL {
			rollbackBatch = append(rollbackBatch, it)
		}
	}
	if len(rollbackBatch) > 0 {
		// Пробуем откатиться, раз что-то уже закоммичено в БД
		tokens := make([]string, 0, len(rollbackBatch))
		for _, it := range rollbackBatch {
			tokens = append(tokens, it.Token)
		}

		rollbackErr := s.config.Storage.DeleteByTokens(ctx, tokens)
		if rollbackErr != nil {
			// Серьезная ошибка
			errs = append(errs, rollbackErr)
		}
	}
	return errors.Join(errs...)
}

// ListUserURLs запрашивает из хранилища все записи вида (shortenURL, originalURL),
// сохраненные пользователем userID за время его жизни
func (s *Shortener) ListUserURLs(ctx context.Context, userID string) (model.UserURLsRes, error) {
	items, err := s.config.Storage.ListUserURLs(ctx, userID)
	if err != nil {
		return model.UserURLsRes{}, fmt.Errorf("ошибка хранилища: %w", err)
	}

	// Сервис мапит записи слоя данных в DTO ответа API.
	res := make(model.UserURLsRes, len(items))
	for i, item := range items {
		res[i] = model.UserURLsResItem{
			ShortURL:    item.ShortURL,
			OriginalURL: item.OriginalURL,
		}
	}
	return res, nil
}

// MarkUserURLsDeleted ставит токены из набора batch в очередь на удаление из хранилища.
// Если очередь переполнена (что моделирует высокую нагрузку на хранилище), возвращает ошибку ErrTokenLeftUndeleted
// для каждого непопавшего токена из batch'а.
func (s *Shortener) MarkUserURLsDeleted(ctx context.Context, batch model.TokensToMarkDeleted, userID string) error {
	if len(batch) == 0 {
		return nil
	}

	req := make(repository.ToMarkDeletedReqBatch, 0, len(batch))
	for i := range batch {
		req = append(req, repository.ToMarkDeletedReqItem{
			Token:  batch[i],
			UserID: userID,
		})
	}

	return s.putInDeletionQueue(req)
}

// putInDeletionQueue помещает пакет в очередь удалений; при переполнении
// очереди или её закрытии элементы получают ErrTokenLeftUndeleted.
func (s *Shortener) putInDeletionQueue(batch repository.ToMarkDeletedReqBatch) error {
	s.deletionQueueMutex.RLock()
	defer s.deletionQueueMutex.RUnlock()

	// Очередь уже закрыта (graceful shutdown истёк по таймауту,
	// а хендлеры всё ещё в полёте): отдаем пакет в dead-letter штатным
	// путём ошибки вместо паники «send on closed channel»
	if s.deletionQueueClosed {
		errs := make([]error, 0, len(batch))
		for _, it := range batch {
			errs = append(errs, NewErrTokenLeftUndeleted(it.Token, it.UserID))
		}
		return errors.Join(errs...)
	}

	errs := make([]error, 0, len(batch))
	for _, it := range batch {
		select {
		case s.deletionQueue <- it:
			// nop
		default:
			errs = append(errs, NewErrTokenLeftUndeleted(it.Token, it.UserID))
		}
	}
	return errors.Join(errs...)
}

// StartItemsDeletionProcessor запускает горутину-процессор удалений:
// накапливает батч до DeletionBatchSize или таймаута и передаёт в хранилище.
// Блокируется до завершения; останов — по отмене контекста.
func (s *Shortener) StartItemsDeletionProcessor(ctx context.Context) {
	batch := make(repository.ToMarkDeletedReqBatch, 0, s.config.DeletionBatchSize)
	ticker := time.NewTicker(s.config.DeletionQueueFlushTimeout)
	defer ticker.Stop()

	// flushCtx переживает отмену сигнального ctx: отложенные удаления
	// доводятся до хранилища даже при останове сервиса по SIGTERM,
	// пока жив пул соединений (он закрывается позже — см. main)
	flushCtx := context.WithoutCancel(ctx)

	var storageWg sync.WaitGroup

	flush := func() {
		if len(batch) == 0 {
			return
		}
		batchToStore := make(repository.ToMarkDeletedReqBatch, len(batch))
		copy(batchToStore, batch)
		batch = batch[:0]

		storageWg.Add(1)
		go func(batch repository.ToMarkDeletedReqBatch) {
			defer storageWg.Done()

			if err := s.config.Storage.MarkUserURLsDeleted(flushCtx, batch); err != nil {
				s.log.Error("ошибка хранилища при выполнении запроса пакетного удаления записей", zap.Error(err))
				// Сохраняем в очередь данные, которые не удалось записать
				s.writeToDeadLetterLog(NewUndeletedItemsFromReqBatch(batch))

				// TODO
				// Точка расширения:
				// * ввести единый канал для сбора UndeleteItem;
				// * вызывать writeToDeadLetterLog при чтении из канала
			}
		}(batchToStore)
	}

	for {
		select {
		case item, ok := <-s.deletionQueue:
			if !ok {
				flush()
				storageWg.Wait()
				return
			}
			batch = append(batch, item)
			if len(batch) >= s.config.DeletionBatchSize {
				flush()
				ticker.Reset(s.config.DeletionQueueFlushTimeout)
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			flush()
			storageWg.Wait()
			return
		}
	}
}

// writeToDeadLetterLog дозаписывает потерянные записи в dead-letter-файл,
// открывая его при необходимости; закрывается CleanUp.
func (s *Shortener) writeToDeadLetterLog(batch []UndeletedItem) error {
	s.deadLetterLogFileMutex.Lock()
	defer s.deadLetterLogFileMutex.Unlock()

	// TODO
	//
	// По-хорошему, надо бы поднять отдельный канал для накопления ошибок с UndeleteItem,
	// тогда можно будет обойтись без mutex'а, но за нехваткой времени на отладку соответствующей инфраструктуры
	// оставляю решение с mutex.

	if s.deadLetterLogFile == nil {
		dllf, err := os.OpenFile(s.config.DeadLetterLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("критическая ошибка: не удалось открыть лог ошибок при запросах на удаление: %w", err)
		}
		s.deadLetterLogFile = dllf
	}

	encoder := json.NewEncoder(s.deadLetterLogFile)
	now := time.Now()
	for _, item := range batch {
		item.Discarded = now
		if err := encoder.Encode(item); err != nil {
			return fmt.Errorf("критическая ошибка: не удалось сериализовать потерянную запись %v: %w", item, err)
		}
	}

	return nil
}

// ErrTokenLeftUndeleted — токен пользователя не попал в очередь на удаление.
type ErrTokenLeftUndeleted struct {
	Token  string
	UserID string
}

// Error реализует интерфейс error.
func (e *ErrTokenLeftUndeleted) Error() string {
	return fmt.Sprintf("токен %v пользователя %v не был добавлен в очередь на удаление", e.Token, e.UserID)
}

// NewErrTokenLeftUndeleted создаёт ошибку для потерянной пары (токен, пользователь).
func NewErrTokenLeftUndeleted(token, userID string) error {
	return &ErrTokenLeftUndeleted{
		Token:  token,
		UserID: userID,
	}
}

// HandleErrTokenLeftUndeletedAndLogData собирает из ошибки все ErrTokenLeftUndeleted
// и дозаписывает их в dead-letter-файл.
func (s *Shortener) HandleErrTokenLeftUndeletedAndLogData(err error) error {
	if err == nil {
		return nil
	}

	var leftItems []UndeletedItem

	// Нормализуем ошибку в плоский список:
	// errors.Join возвращает набор ошибок с методом Unwrap() []error,
	// одиночная ошибка — список из одного элемента.
	var innerErrs []error
	if unwrapped, ok := err.(interface{ Unwrap() []error }); ok {
		innerErrs = unwrapped.Unwrap()
	} else {
		innerErrs = []error{err}
	}

	// errors.As по набору останавливается на первом вхождении,
	// поэтому обходим список циклом и собираем все записи.
	for _, innerErr := range innerErrs {
		var target *ErrTokenLeftUndeleted
		if errors.As(innerErr, &target) {
			leftItems = append(leftItems, UndeletedItem{
				Token:  target.Token,
				UserID: target.UserID,
			})
		}
	}

	return s.writeToDeadLetterLog(leftItems)

	// TODO
	// Точка расширения:
	// * ввести единый канал для сбора UndeleteItem;
	// * вызывать writeToDeadLetterLog при чтении из канала
}

// UndeletedItem — запись dead-letter-лога: пара (токен, пользователь)
// и момент, когда она была отброшена.
type UndeletedItem struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Discarded time.Time `json:"discarded_at,omitzero"`
}

// NewUndeletedItemsFromReqBatch конвертирует пакет запросов на удаление
// в записи dead-letter-лога.
func NewUndeletedItemsFromReqBatch(batch repository.ToMarkDeletedReqBatch) []UndeletedItem {
	res := make([]UndeletedItem, 0, len(batch))

	for _, it := range batch {
		res = append(res, UndeletedItem{
			Token:  it.Token,
			UserID: it.UserID,
		})
	}

	return res
}

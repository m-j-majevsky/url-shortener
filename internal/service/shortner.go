package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/m-j-majevsky/url-shortener/internal/crypto"
	"github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/logger"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
	"go.uber.org/zap"
)

type BasicStorage interface {
	Store(ctx context.Context, token, longURL, userID string) error
	Resolve(ctx context.Context, token string) (string, error)
	BatchStore(ctx context.Context, batch repository.StoreBatch, userID string) (repository.StoreBatch, error)
	ListUserURLs(ctx context.Context, userID string) (model.UserURLsRes, error)
	MarkUserURLsDeleted(ctx context.Context, batch repository.ToMarkDeletedReqBatch) error
}

type StorageCleaner interface {
	DeleteByTokens(ctx context.Context, tokens []string) error
}

type StoragePinger interface {
	Ping(ctx context.Context) error
}

type ShortenerConfig struct {
	Storage BasicStorage

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

}

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

type Shortener struct {
	config                 ShortenerConfig
	deletionQueue          chan repository.ToMarkDeletedReqItem
	deadLetterLogFile      *os.File
	deadLetterLogFileMutex sync.Mutex
}

const errCfgHeader = "ошибка конфигурации сервиса"

func NewShortener(cfg ShortenerConfig) (*Shortener, error) {
	if cfg.Storage == nil {
		return nil, fmt.Errorf("%s: не задано хранилище", errCfgHeader)
	}
	if cfg.RandProv == nil {
		return nil, fmt.Errorf("%s: не задан генератор случайных данных", errCfgHeader)
	}
	if cfg.BytesToGenerate <= 0 {
		return nil, fmt.Errorf("%s: число генерируемых байт должно быть неотрицательно", errCfgHeader)
	}
	if cfg.MinTokenLength <= 0 || cfg.MaxTokenLength <= 0 {
		return nil, fmt.Errorf("%s: границы длин токена должны быть неотрицательны", errCfgHeader)
	}
	if cfg.MinTokenLength > cfg.MaxTokenLength {
		return nil, fmt.Errorf("%s: наименьшая допустимая длина токена не должна превосходить наибольшую", errCfgHeader)
	}
	if cfg.MaxGeneratingAttempts <= 0 {
		return nil, fmt.Errorf("%s: количество попыток создать уникальный токен должно быть неотрицательно", errCfgHeader)
	}
	if cfg.MaxStoringAttempts <= 0 {
		return nil, fmt.Errorf("%s: количество попыток сделать запись в хранилище должно быть неотрицательно", errCfgHeader)
	}
	if cfg.DeletionBatchSize <= 0 {
		return nil, fmt.Errorf("%s: размер батча удаляемых за раз записей должен быть неотрицательным", errCfgHeader)
	}

	shortener := &Shortener{
		config:        cfg,
		deletionQueue: make(chan repository.ToMarkDeletedReqItem, cfg.DeletionQueueBuffer),
	}

	return shortener, nil
}

func (s *Shortener) StopItemsDeletionProcessor() {
	close(s.deletionQueue)
}

func (s *Shortener) CleanUp() {
	s.deadLetterLogFileMutex.Lock()
	defer s.deadLetterLogFileMutex.Unlock()

	if s.deadLetterLogFile != nil {
		s.deadLetterLogFile.Close()
	}
}

// generateTokens генериурет num различных base62-токенов,
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

func (s *Shortener) GenerateAndStore(ctx context.Context, longURL, userID string) (string, error) {
	for i := 0; i < s.config.MaxStoringAttempts; i++ {
		tokens, err := s.generateTokens(1)
		if err != nil {
			// Ошибка библиотечного генератора, контекста,
			// или не удалось выполнить ограничения на токен из конфига
			//
			// Ошибку дополнительно не оборачиваю, т.к. все обертки сделаны в GenerateToken
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
			return eoue.StoredToken, eoue
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

func (s *Shortener) Resolve(ctx context.Context, token string) (string, error) {
	url, err := s.config.Storage.Resolve(ctx, token)
	if err != nil {
		return "", fmt.Errorf("ошибка хранилища: %w", err)
	}
	return url, nil
}

func (s *Shortener) Ping(ctx context.Context) error {
	swp, ok := s.config.Storage.(StoragePinger)
	if !ok {
		return fmt.Errorf("хранилище не поддерживает метод Ping")
	}
	return swp.Ping(ctx)
}

func (s *Shortener) GetConfig() ShortenerConfig {
	return s.config
}

func (s *Shortener) BatchStore(ctx context.Context, req model.BatchShortenReq, userID string) (model.BatchShortenRes, error) {
	reqLen := len(req)
	if reqLen == 0 {
		return model.BatchShortenRes{}, nil
	}

	// batch вводится для внутреннего представления
	// и обработки содержимого входного пакета в методах сервиса и хранилища
	batch := repository.NewStoreBatch(req)
	// В stored накапливаем успешно сохраненные в БД элеметны batch
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

		// Собираем успешно записаные или имевшие конфликт по URL запросы
		stored = appendStoredItems(stored, storageRes)

		if len(stored) == reqLen {
			// Проблемных элементов нет; переходим к оформлению ответа
			break
		}

		var ett *repository.ErrTokenTaken
		if errors.As(err, &ett) {
			// Есть занятыe токены для некоторых записей, возвращенных в storageRes;
			// для них предпримем новую попытку генерации токенов и сохранения,
			// предварительно подготовив данные на итерацию
			batch = batchForNextAttempt(storageRes)
			continue
		}

		// Прочие ошибки от БД отдаем наверх, предварительно предриняв попытку зачистить успешно закоммиченные данные
		return model.BatchShortenRes{}, s.restoreWithError(ctx, stored, fmt.Errorf("ошибка сохранения пакета: %w", err))
	} // for on nAttempts

	return buildBatchShortenRes(stored), nil
}

// assignTokensToBatch распихивает по пакету req токены из tokens.
//
// Модифицирует первый агрумент req.
//
// Вызывающий код ответственен за то, чтобы в слайсе tokens было
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
// В случае успешного удаления возвращает errToReturn.
// В случае ошибки отката, вовзращает errors.Join ошибки errToReturn и ошибки,
// не позволившей выполнить откат.
func (s *Shortener) restoreWithError(ctx context.Context, toRollback repository.StoreBatch, errToReturn error) error {
	errs := []error{errToReturn}
	if len(toRollback) > 0 {
		// Пробуем откатиться, раз что-то уже закоммичено в БД
		tokens := make([]string, 0, len(toRollback))
		for _, it := range toRollback {
			tokens = append(tokens, it.Token)
		}

		sc, ok := s.config.Storage.(StorageCleaner)
		if !ok {
			// Серьезная ошибка
			errs = append(errs, fmt.Errorf("невозможно получить доступ к методу зачистки мусора из хранилища"))
		}

		rollbackErr := sc.DeleteByTokens(ctx, tokens)
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
	res, err := s.config.Storage.ListUserURLs(ctx, userID)
	if err != nil {
		return model.UserURLsRes{}, fmt.Errorf("ошибка хранилища: %w", err)
	}
	if res == nil {
		res = model.UserURLsRes{}
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

func (s *Shortener) putInDeletionQueue(batch repository.ToMarkDeletedReqBatch) error {
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

func (s *Shortener) StartItemsDeletionProcessor(ctx context.Context) {
	batch := make(repository.ToMarkDeletedReqBatch, 0, s.config.DeletionBatchSize)
	ticker := time.NewTicker(s.config.DeletionQueueFlushTimeout)
	defer ticker.Stop()

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

			if err := s.config.Storage.MarkUserURLsDeleted(ctx, batch); err != nil {
				logger.Log.Info("storage error while running batch item deletion query", zap.Error(err))
				// Сохраняем в очередь данные, которые не удалось записать
				s.writeToDeadLetterLog(NewUndeletedItemsFromReqBatch(batch))

				// TODO
				// Точка расширения:
				// * ввести единый канал для сбора UndeleteItem;
				// * вызывавать writeToDeadLetterLog при чтении из канала
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

func (s *Shortener) writeToDeadLetterLog(batch []UndeletedItem) error {
	s.deadLetterLogFileMutex.Lock()
	defer s.deadLetterLogFileMutex.Unlock()

	// TODO
	//
	// По-хорошему, надо бы поднять отдельный канал для накопления ошибок с UndeleteItem,
	// тогда можно будет обойтись без mutex'а, но за нехваткой времени на отладку соответствующей инфрасруктуры
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

type ErrTokenLeftUndeleted struct {
	Token  string
	UserID string
}

func (e *ErrTokenLeftUndeleted) Error() string {
	return fmt.Sprintf("токен %v пользователя %v не был добавлен в очередь на удаление", e.Token, e.UserID)
}

func NewErrTokenLeftUndeleted(token, userID string) error {
	return &ErrTokenLeftUndeleted{
		Token:  token,
		UserID: userID,
	}
}

func (s *Shortener) HandleErrTokenLeftUndeletedAndLogData(err error) error {
	if err == nil {
		return nil
	}

	var leftItems []UndeletedItem

	if unwrapped, ok := err.(interface{ Unwrap() []error }); ok {
		// Случай набора ErrTokenLeftUndeleted для разных записей
		for _, innerErr := range unwrapped.Unwrap() {
			var target *ErrTokenLeftUndeleted
			if errors.As(innerErr, &target) {
				leftItems = append(leftItems, UndeletedItem{
					Token:  target.Token,
					UserID: target.UserID,
				})
			}
		}
	} else {
		// Случай одной ошибки ErrTokenLeftUndeleted в наборе
		var target *ErrTokenLeftUndeleted
		if errors.As(err, &target) {
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
	// * вызывавать writeToDeadLetterLog при чтении из канала
}

type UndeletedItem struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Discarded time.Time `json:"discarded_at,omitzero"`
}

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

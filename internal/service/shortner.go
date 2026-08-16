package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/m-j-majevsky/url-shortener/internal/crypto"
	"github.com/m-j-majevsky/url-shortener/internal/encoding"
	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

type BasicStorage interface {
	Store(ctx context.Context, token string, longURL string) error
	Resolve(ctx context.Context, token string) (string, error)
	BatchStore(ctx context.Context, batch repository.Batch) (repository.Batch, error)
	DeleteByTokens(ctx context.Context, tokens []string) error
}

type StorageWithDB interface {
	BasicStorage
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
}

func DefaultShortenerConfig() ShortenerConfig {
	return ShortenerConfig{
		// Storage не устанавливается!
		RandProv:              crypto.CryptoRandProvider{},
		MinTokenLength:        6,
		MaxTokenLength:        10,
		BytesToGenerate:       6,
		MaxGeneratingAttempts: 10,
		MaxStoringAttempts:    10,
	}
}

type Shortener struct {
	config ShortenerConfig
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

	return &Shortener{config: cfg}, nil
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

func (s *Shortener) GenerateAndStore(ctx context.Context, longURL string) (string, error) {
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
		err = s.config.Storage.Store(ctx, token, longURL)

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
			// отличные от "токен занят", отдаем наверх
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

func (s *Shortener) PingDB(ctx context.Context) error {
	swp, ok := s.config.Storage.(StorageWithDB)
	if !ok {
		return fmt.Errorf("хранилище не поддерживает метод Ping")
	}
	return swp.Ping(ctx)
}

func (s *Shortener) WithDB() bool {
	_, ok := s.config.Storage.(StorageWithDB)
	return ok
}

func (s *Shortener) BatchStore(ctx context.Context, req model.BatchShortenReq) (model.BatchShortenRes, error) {
	lenReq := len(req)
	if lenReq == 0 {
		return model.BatchShortenRes{}, nil
	}

	// batchReq вводится для внутреннего представления
	// и обработки содержимого входного пакета в методах сервиса и хранилища
	batchReq := repository.NewBatch(req)
	// В processed накапливаем успешно сохраненные в БД элеметны batchReq
	processed := make(repository.Batch, 0, lenReq)
	// В numTok хранится количество требующихся случайных токенов для очередной итерации цикла
	numTok := lenReq
	// Отсчет попыток сохранения до успешной или до достижения лимита попыток
	for i := 0; i < s.config.MaxStoringAttempts; i++ {
		tokens, err := s.generateTokens(numTok)
		if err != nil {
			// Ошибки механизма генерации токенов
			// err дополнительно не оборачиваю, т.к. все обертки сделаны в generateTokens
			return model.BatchShortenRes{}, s.restoreWithError(ctx, processed, err)
		}

		// Распихиваем по пакету сгенерированные токены
		for i := range batchReq {
			batchReq[i].Token = tokens[i]
		}

		batchRes, err := s.config.Storage.BatchStore(ctx, batchReq)
		if err == nil {
			// Попытка записи удалась, можно переходить к оформлению ответа
			processed = append(processed, batchRes...)
			break
		}

		// Далее до конца блока выполняется err != nil,
		// поэтому обработка зависит от специфики ошибки

		var eoue *repository.ErrOriginalURLExists
		if errors.As(err, &eoue) {
			// Имеют место случаи конфлитка по исходному URL.
			for i := range batchRes {
				if batchRes[i].ConflictedURL {
					// Данные о сохраненном токене есть в поле BatchItem.TokenOnConflictedURL
					batchRes[i].Token = batchRes[i].TokenOnConflictedURL
				}
			}
		}

		for _, it := range batchRes {
			if it.ConflictedURL || !it.ConflictedToken {
				// добавляем в список успешно обработанных (processed)
				processed = append(processed, it)
			}
		}
		if len(processed) == lenReq {
			// Все данные собраны в processed; переходим к оформлению ответа
			break
		}

		var ett *repository.ErrTokenTaken
		if errors.As(err, &ett) {
			// Есть занятыe токены для некоторых записей, возвращенных в batchRes;
			// для них предпримем новую попытку генерации токенов и сохранения,
			// предварительно подготовив данные на итерацию
			batchReq = batchReq[:0]
			numTok = 0 // сколько токенов надо будет сгенерить на следующей итерации
			for _, it := range batchRes {
				if it.ConflictedToken && !it.ConflictedURL {
					batchItem := repository.BatchItem{
						CorrelationID: it.CorrelationID,
						OriginalURL:   it.OriginalURL,
					}
					batchReq = append(batchReq, batchItem)
					numTok++
				}
			}
			continue
		}

		// Прочие ошибки от БД отдаем наверх, предварительно предриняв попытку зачистить успешно закоммиченные данные
		return model.BatchShortenRes{}, s.restoreWithError(ctx, processed, fmt.Errorf("ошибка сохранения пакета: %w", err))
	} // for

	res := make(model.BatchShortenRes, lenReq)
	for i := range processed {
		res[i].CorrelationID = processed[i].CorrelationID
		res[i].ShortURL = processed[i].Token
		res[i].ConflictedURL = processed[i].ConflictedURL
	}

	return res, nil
}

// restoreWithError пробует удалить данные из батча toRollback из хранилища.
// В случае успешного удаления возвращает errToReturn.
// В случае ошибки отката, вовзращает errors.Join ошибки errToReturn и ошибки,
// не позволившей выполнить откат.
func (s *Shortener) restoreWithError(ctx context.Context, toRollback repository.Batch, errToReturn error) error {
	errs := []error{errToReturn}
	if len(toRollback) > 0 {
		// Пробуем откатиться, раз что-то уже закоммичено в БД
		tokens := make([]string, 0, len(toRollback))
		for _, it := range toRollback {
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

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
	Store(ctx context.Context, token string, longURL model.URL) error
	Resolve(ctx context.Context, token string) (model.URL, error)
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
		return []string{}, fmt.Errorf("ошибка при генерации %d различных токенов(-а) с длиной в диапазоне [%d,%d] за %d попыток",
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
		err = s.config.Storage.Store(ctx, token, model.NewURL(longURL))
		if err == nil {
			// Успех
			return token, nil
		}
		var ett *repository.ErrTokensTaken
		if !errors.As(err, &ett) {
			// Прочие возможные ошибки репозитория или контекста, отличные от "токен занят"
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
	return url.String(), nil
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

func (s *Shortener) BatchStore(ctx context.Context, req model.BatchSortenReq) (model.BatchSortenRes, error) {
	lenReq := len(req)
	if lenReq == 0 {
		return model.BatchSortenRes{}, nil
	}

	batchReq := repository.NewBatch(req)
	processed := make(repository.Batch, 0, lenReq)
	numTok := lenReq
	// Отсчет попыток сохранения до успешной или до достижения лимита попыток
	for i := 0; i < s.config.MaxStoringAttempts; i++ {
		tokens, err := s.generateTokens(numTok)
		if err != nil {
			// Ошибки механизма генерации токенов
			// err дополнительно не оборачиваю, т.к. все обертки сделаны в generateTokens
			return model.BatchSortenRes{}, s.restoreWithError(ctx, processed, err)
		}

		for i := range batchReq {
			batchReq[i].Token = tokens[i]
		}

		batchRes, err := s.config.Storage.BatchStore(ctx, batchReq)
		if err == nil {
			// Попытка записи удалась.
			// Можно переходить к оформлению ответа
			processed = append(processed, batchRes...)
			break
		}

		// Далее до конца блока err != nil, поэтому обработка зависит от специфики ошибки
		var ett *repository.ErrTokensTaken
		if errors.As(err, &ett) {
			// Есть занятыe токены; для соответствующих записей
			// пробуем предпринять новую попытку генерации токенов и сохранения
			batchReq = batchReq[:0]
			numTok = 0
			for _, it := range batchRes {
				if it.ConflictedToken {
					// Готовим запись к следующей попытки генерации-записи
					it.ConflictedToken = false
					it.Token = ""
					batchReq = append(batchReq, it)
					numTok++
				} else {
					// Закомиченную запись не забываем добавить в список успешно обработанных
					processed = append(processed, it)
				}
			}
			continue
		}

		// Прочие ошибки от БД отдаем наверх
		return model.BatchSortenRes{}, s.restoreWithError(ctx, processed, fmt.Errorf("ошибка сохранения пакета: %w", err))
	}

	res := make(model.BatchSortenRes, lenReq)
	for i := range processed {
		res[i].CorrelationID = processed[i].CorrelationID
		res[i].ShortURL = model.URL(processed[i].Token)
	}

	return res, nil
}

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

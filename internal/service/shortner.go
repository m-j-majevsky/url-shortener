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

func (s *Shortener) GenerateToken(ctx context.Context) (string, error) {
	cfg := s.config

	for attempt := 0; attempt < cfg.MaxGeneratingAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("генерация токена прервана из-за отмены контекста: %w", err)
		}

		bytes, err := crypto.GenerateRandomBytes(s.config.RandProv, cfg.BytesToGenerate)
		if err != nil {
			return "", err
		}

		token := encoding.EncodeToBase62(bytes)

		if len(token) >= cfg.MinTokenLength && len(token) <= cfg.MaxTokenLength {
			return token, nil
		}
	}
	// Не удалось уложиться в заданный диапазон длин
	return "", fmt.Errorf("ошибка генерации токена с длиной в диапазоне [%d,%d] за %d попыток",
		cfg.MinTokenLength, cfg.MaxTokenLength, cfg.MaxGeneratingAttempts)
}

func (s *Shortener) GenerateAndStore(ctx context.Context, longURL string) (string, error) {
	for i := 0; i < s.config.MaxStoringAttempts; i++ {
		token, err := s.GenerateToken(ctx)
		if err != nil {
			// Ошибка библиотечного генератора, контекста
			// или не удалось выполнить ограничения на токен из конфига
			return "", err
		}

		err = s.config.Storage.Store(ctx, token, model.NewURL(longURL))
		if err == nil {
			// Успех
			return token, nil
		}
		var ett *repository.ErrTokenTaken
		if !errors.As(err, &ett) {
			// Прочие возможные ошибки репозитория или контекста, отличные от "токен занят"
			return "", err
		}
	}

	return "", fmt.Errorf("не удалось сохранить URL за %d попыток", s.config.MaxStoringAttempts)
}

func (s *Shortener) Resolve(ctx context.Context, token string) (string, error) {
	url, err := s.config.Storage.Resolve(ctx, token)
	return url.String(), err
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

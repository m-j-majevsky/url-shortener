package service

import (
	"errors"

	"github.com/m-j-majevsky/url-shortener/internal/model"
	"github.com/m-j-majevsky/url-shortener/internal/repository"
)

var ErrShortenURL = errors.New("service: ошибка сохранения")

func ShortenURL(s repository.URLStorage, longURL string) (string, error) {
	shortURL, err := s.ShortenAndStore(model.NewLongURL(longURL))
	if err != nil {
		return "", errors.Join(ErrShortenURL, err)
	}
	return shortURL.String(), nil
}

func ResolveShortURL(s repository.URLStorage, shortURL string) (string, bool) {
	longURL, ok := s.Resolve(model.NewShortURL(shortURL))
	return longURL.String(), ok
}

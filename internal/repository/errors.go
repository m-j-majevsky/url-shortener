package repository

import "fmt"

type (
	// Ошибка, когда сохраняемый токен уже выдан другому URL'у
	ErrTokenTaken struct {
		Token string
	}

	// Ошибка, если запрашиваемый токен не найден
	ErrTokenNotFound struct {
		Token string
	}

	// Ошибка, когда сокращаемый URL уже есть в хранилице
	ErrOriginalURLExists struct {
		StoredToken string
		URL         string
	}

	ErrTokenIsDeleted struct {
		Token string
	}
)

// Методы ErrTokenTaken

func NewErrTokenTaken(token string) error {
	return &ErrTokenTaken{
		Token: token,
	}
}

func (e *ErrTokenTaken) Error() string {
	return fmt.Sprintf("занятый токен: %s", e.Token)
}

// Методы ErrTokenNotFound

func NewErrTokenNotFound(tok string) error {
	return &ErrTokenNotFound{
		Token: tok,
	}
}

func (e *ErrTokenNotFound) Error() string {
	return fmt.Sprintf("токен %s не найден", e.Token)
}

// Методы ErrOriginalURLExists

func NewErrOriginalURLExists(token, url string) error {
	return &ErrOriginalURLExists{
		StoredToken: token,
		URL:         url,
	}
}

func (e *ErrOriginalURLExists) Error() string {
	return fmt.Sprintf("URL %s сохранен под токеном %s", e.URL, e.StoredToken)
}

// Методы ErrTokenIsDeleted

func NewErrTokenIsDeleted(tok string) error {
	return &ErrTokenIsDeleted{
		Token: tok,
	}
}

func (e *ErrTokenIsDeleted) Error() string {
	return fmt.Sprintf("токен %s удален", e.Token)
}

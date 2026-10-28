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

	// Ошибка, когда сокращаемый URL уже есть в хранилище
	ErrOriginalURLExists struct {
		StoredToken string
		URL         string
	}

	// ErrTokenIsDeleted — токен был удалён по запросу пользователя.
	ErrTokenIsDeleted struct {
		Token string
	}
)

// Методы ErrTokenTaken

// NewErrTokenTaken создаёт ошибку занятого токена.
func NewErrTokenTaken(token string) error {
	return &ErrTokenTaken{
		Token: token,
	}
}

// Error реализует интерфейс error.
func (e *ErrTokenTaken) Error() string {
	return fmt.Sprintf("занятый токен: %s", e.Token)
}

// Методы ErrTokenNotFound

// NewErrTokenNotFound создаёт ошибку ненайденного токена.
func NewErrTokenNotFound(tok string) error {
	return &ErrTokenNotFound{
		Token: tok,
	}
}

// Error реализует интерфейс error.
func (e *ErrTokenNotFound) Error() string {
	return fmt.Sprintf("токен %s не найден", e.Token)
}

// Методы ErrOriginalURLExists

// NewErrOriginalURLExists создаёт ошибку конфликта: URL уже сохранён под StoredToken.
func NewErrOriginalURLExists(token, url string) error {
	return &ErrOriginalURLExists{
		StoredToken: token,
		URL:         url,
	}
}

// Error реализует интерфейс error.
func (e *ErrOriginalURLExists) Error() string {
	return fmt.Sprintf("URL %s сохранен под токеном %s", e.URL, e.StoredToken)
}

// Методы ErrTokenIsDeleted

// NewErrTokenIsDeleted создаёт ошибку удалённого токена.
func NewErrTokenIsDeleted(tok string) error {
	return &ErrTokenIsDeleted{
		Token: tok,
	}
}

// Error реализует интерфейс error.
func (e *ErrTokenIsDeleted) Error() string {
	return fmt.Sprintf("токен %s удален", e.Token)
}

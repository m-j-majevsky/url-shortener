package repository

import "fmt"

type (
	// ErrTokenTaken — сохраняемый токен уже выдан другому URL.
	// Возвращается Store и BatchStore при конфликте по токену.
	ErrTokenTaken struct {
		Token string
	}

	// ErrTokenNotFound — запрашиваемый токен в хранилище не найден.
	// Возвращается Resolve.
	ErrTokenNotFound struct {
		Token string
	}

	// ErrOriginalURLExists — сокращаемый URL уже сохранён в хранилище
	// под другим токеном StoredToken. Возвращается Store и BatchStore
	// при конфликте по original_url.
	ErrOriginalURLExists struct {
		StoredToken string
		URL         string
	}

	// ErrTokenIsDeleted — токен был удалён по запросу пользователя.
	ErrTokenIsDeleted struct {
		Token string
	}
)

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

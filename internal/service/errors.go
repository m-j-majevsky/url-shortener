package service

import "fmt"

// Ошибки сервисного слоя.
//
// Правило слоёв: наверх (в хендлеры) протекают только ошибки сервиса,
// типы хранилища наружу не выходят. Каждая ошибка — типизированная
// структура с полями-носителями данных (по образцу repository.Err*),
// которую хендлер проверяет через errors.As и использует для выбора
// HTTP-кода ответа и формирования тела.

type (
	// ErrTokenNotFound — запрашиваемый токен не найден.
	ErrTokenNotFound struct {
		Token string
	}

	// ErrTokenIsDeleted — токен был удалён по запросу пользователя.
	ErrTokenIsDeleted struct {
		Token string
	}

	// ErrOriginalURLExists — сокращаемый URL уже сохранён ранее.
	ErrOriginalURLExists struct {
		StoredToken string
		URL         string
	}
)

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

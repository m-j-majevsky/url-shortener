package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

type ErrTokenInvalid struct {
	Err error
}

func (e *ErrTokenInvalid) Error() string {
	msg := "токен невалиден"
	if e.Err != nil {
		msg = fmt.Sprintf("%s: %v", msg, e.Err)
	}
	return msg
}

func NewErrTokenInvalid(err error) error {
	return &ErrTokenInvalid{Err: err}
}

func ParseUserIDJWT(cookieValue string, jwtSecret []byte) (string, error) {
	return parseUserIDJWT([]byte(cookieValue), getSecureKeyFunc(jwtSecret))
}

func getSecureKeyFunc(jwtSecret []byte) jwt.Keyfunc {
	return func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("недопустимый метод подписания: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	}
}

type Claims struct {
	jwt.RegisteredClaims
	UserID string `json:"user_id"`
}

func parseUserIDJWT(jwtBytes []byte, keyFunc jwt.Keyfunc) (string, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(string(jwtBytes), claims, keyFunc)
	if err != nil {
		return "", NewErrTokenInvalid(err)
	}
	if !token.Valid {
		return "", NewErrTokenInvalid(nil)
	}

	return claims.UserID, nil
}

func GenerateUserIDJWT(userID string, ttl time.Duration, jwtSecret []byte) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
		UserID: userID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

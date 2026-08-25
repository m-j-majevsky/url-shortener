package handler

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/m-j-majevsky/url-shortener/internal/encrypting"
)

type errTokenInvalid struct {
	Err error
}

func (e *errTokenInvalid) Error() string {
	msg := "токен невалиден"
	if e.Err != nil {
		msg = fmt.Sprintf("%s: %v", msg, e.Err)
	}
	return msg
}

func newErrTokenInvalid(err error) error {
	return &errTokenInvalid{Err: err}
}

func decryptAndParseJWT(encryptedB64 string, aesKey []byte, jwtSecret []byte) (string, error) {
	cipherbytes, err := base64.URLEncoding.DecodeString(encryptedB64)
	if err != nil {
		return "", err
	}

	jwtBytes, err := encrypting.DecryptAESGCM(cipherbytes, aesKey)
	if err != nil {
		return "", err
	}

	return parseUserIDJWT(jwtBytes, getSecureKeyFunc(jwtSecret))
}

func getSecureKeyFunc(jwtSecret []byte) jwt.Keyfunc {
	return func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("недопустимый метод подписания: %v", t.Header["alg"])
		}
		return jwtSecret, nil
	}
}

func parseUserIDJWT(jwtBytes []byte, keyFunc jwt.Keyfunc) (string, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(string(jwtBytes), claims, keyFunc)
	if err != nil {
		return "", newErrTokenInvalid(err)
	}
	if !token.Valid {
		return "", newErrTokenInvalid(nil)
	}

	return claims.UserID, nil
}

func generateEncryptedJWT(userID string, ttl time.Duration, aesKey []byte, jwtSecret []byte) (string, error) {
	token, err := buildUserIDSignedJWT(userID, ttl, jwtSecret)
	if err != nil {
		return "", err
	}

	cipherbytes, err := encrypting.EncryptAESGCM([]byte(token), aesKey)
	if err != nil {
		return "", err
	}

	return base64.URLEncoding.EncodeToString(cipherbytes), nil
}

func buildUserIDSignedJWT(userID string, ttl time.Duration, jwtSecret []byte) (string, error) {
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

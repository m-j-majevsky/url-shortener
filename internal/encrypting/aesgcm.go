package encrypting

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

func EncryptAESGCM(messagebytes []byte, aesKey []byte) ([]byte, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return []byte{}, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return []byte{}, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return []byte{}, err
	}

	return gcm.Seal(nonce, nonce, messagebytes, nil), nil
}

func DecryptAESGCM(cipherbytes []byte, aesKey []byte) ([]byte, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return []byte{}, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return []byte{}, err
	}

	nonceSize := gcm.NonceSize()
	if len(cipherbytes) < nonceSize {
		return []byte{}, fmt.Errorf("размер шифруемого текста %d слишком короток, "+
			"ожидается текст рамером по крайней мере %d байт", len(cipherbytes), nonceSize)
	}

	nonce, actualCiphertext := cipherbytes[:nonceSize], cipherbytes[nonceSize:]
	result, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return []byte{}, err
	}

	return result, nil
}

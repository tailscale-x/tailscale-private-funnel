package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

func Open(path string) ([]byte, error) {
	b, err := osReadFile(path)
	if err == nil {
		if len(b) != 32 {
			return nil, errors.New("invalid encryption key; restore the original key")
		}
		return b, nil
	}
	if err != nil && !errors.Is(err, errNotExist) {
		return nil, err
	}
	b = make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := osWriteFile(path, b); err != nil {
		return nil, err
	}
	return b, nil
}

func Seal(key []byte, value string) (string, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(value), nil)), nil
}

func OpenValue(key []byte, value string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	c, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(c)
	if err != nil {
		return "", err
	}
	if len(b) < g.NonceSize() {
		return "", errors.New("invalid encrypted value")
	}
	p, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(p), nil
}

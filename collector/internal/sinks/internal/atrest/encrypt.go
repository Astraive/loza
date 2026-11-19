package atrest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const Prefix = "enc:"

var errEmptyKey = errors.New("atrest: encryption key must not be empty")

// ErrMissingPrefix indicates that data passed to DecryptBytes lacked the
// encryption prefix while a decryption key was configured. Returning such data
// verbatim would let an unauthenticated value be treated as trusted plaintext.
var ErrMissingPrefix = errors.New("atrest: data is missing encryption prefix")

func EncryptString(plain []byte, key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errEmptyKey
	}
	aead, err := buildAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, plain, nil)
	return Prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func EncryptBytes(plain []byte, key string) ([]byte, error) {
	text, err := EncryptString(plain, key)
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

func DecryptBytes(data []byte, key string) ([]byte, error) {
	raw := strings.TrimSpace(string(data))
	if !strings.HasPrefix(raw, Prefix) {
		// Plaintext (written with encryption disabled) needs no key. When a
		// key is configured, unprefixed data must not be trusted as plaintext:
		// anything written to the store could otherwise inject a value that
		// bypasses decryption and authentication entirely.
		if strings.TrimSpace(key) != "" {
			return nil, ErrMissingPrefix
		}
		return append([]byte(nil), data...), nil
	}
	if strings.TrimSpace(key) == "" {
		// Encrypted data with no key cannot be decrypted; fail closed rather
		// than returning the undecorated ciphertext as if it were plaintext.
		return nil, errEmptyKey
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, Prefix))
	if err != nil {
		return nil, err
	}
	aead, err := buildAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(payload) < aead.NonceSize() {
		return nil, io.ErrUnexpectedEOF
	}
	nonce := payload[:aead.NonceSize()]
	return aead.Open(nil, nonce, payload[aead.NonceSize():], nil)
}

func buildAEAD(key string) (cipher.AEAD, error) {
	derived := make([]byte, 32)
	reader := hkdf.New(sha256.New, []byte(key), []byte("loza-at-rest-v1"), []byte("aes-256-gcm"))
	if _, err := io.ReadFull(reader, derived); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

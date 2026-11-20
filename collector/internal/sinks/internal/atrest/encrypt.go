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

const (
	// Prefix marks legacy ciphertexts that were sealed with no additional
	// authenticated data. It is retained so data written before context binding
	// was introduced remains decryptable.
	Prefix = "enc:"

	// ContextPrefix marks ciphertexts whose AEAD additional data binds them to
	// the location (field/table/record) they were produced for.
	ContextPrefix = "enc:v2:"

	// DefaultContext is used by the context-free helper functions. Callers that
	// know the storage location should use the *WithContext variants instead so
	// a value cannot be replayed or swapped into a different location.
	DefaultContext = "loza:default"

	aadDomain = "loza-atrest-v1/context/"
)

var errEmptyKey = errors.New("atrest: encryption key must not be empty")

// ErrMissingPrefix indicates that data passed to DecryptBytes lacked the
// encryption prefix while a decryption key was configured. Returning such data
// verbatim would let an unauthenticated value be treated as trusted plaintext.
var ErrMissingPrefix = errors.New("atrest: data is missing encryption prefix")

// contextAAD derives the additional authenticated data that cryptographically
// binds a ciphertext to its location. The identical context must be supplied at
// decryption time; a ciphertext sealed for one context fails authentication
// when opened under another.
func contextAAD(context string) []byte {
	return []byte(aadDomain + context)
}

// EncryptString encrypts plain under key using DefaultContext and returns a
// prefixed, base64-encoded blob. Prefer EncryptStringWithContext so the
// ciphertext is bound to the specific location it is stored in.
func EncryptString(plain []byte, key string) (string, error) {
	return EncryptStringWithContext(plain, key, DefaultContext)
}

// EncryptStringWithContext encrypts plain under key, binding the ciphertext to
// context via AEAD additional authenticated data. An empty key is rejected.
func EncryptStringWithContext(plain []byte, key, context string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errEmptyKey
	}
	aead, err := buildAEAD(key)
	if err != nil {
		return "", err
	}
	sealed, err := sealWithAAD(aead, plain, contextAAD(context))
	if err != nil {
		return "", err
	}
	return ContextPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// EncryptBytes is the byte-oriented form of EncryptString.
func EncryptBytes(plain []byte, key string) ([]byte, error) {
	return EncryptBytesWithContext(plain, key, DefaultContext)
}

// EncryptBytesWithContext is the byte-oriented form of EncryptStringWithContext.
func EncryptBytesWithContext(plain []byte, key, context string) ([]byte, error) {
	text, err := EncryptStringWithContext(plain, key, context)
	if err != nil {
		return nil, err
	}
	return []byte(text), nil
}

// DecryptBytes decrypts a blob produced by EncryptString. It also accepts
// legacy Prefix ciphertexts (sealed with nil AAD) so previously written data
// remains readable; newly written ciphertext is always context bound.
func DecryptBytes(data []byte, key string) ([]byte, error) {
	return DecryptBytesWithContext(data, key, DefaultContext)
}

// DecryptBytesWithContext decrypts a blob, requiring the same context that was
// used to encrypt it. Legacy Prefix ciphertexts are accepted for backward
// compatibility regardless of context because they carry no binding.
func DecryptBytesWithContext(data []byte, key, context string) ([]byte, error) {
	raw := strings.TrimSpace(string(data))
	if !strings.HasPrefix(raw, Prefix) {
		// Plaintext (written with encryption disabled) needs no key. When a
		// key is configured, unprefixed data must not be trusted as plaintext:
		// anything written to the store could otherwise inject a value that
		// bypasses decryption and authentication entirely.
		if strings.TrimSpace(key) != "" {
			return nil, ErrMissingPrefix
		}
	}
	legacy := false
	var body string
	switch {
	case strings.HasPrefix(raw, ContextPrefix):
		body = strings.TrimPrefix(raw, ContextPrefix)
	case strings.HasPrefix(raw, Prefix):
		body = strings.TrimPrefix(raw, Prefix)
		legacy = true
	default:
		// Plaintext (written with encryption disabled) needs no key.
		return append([]byte(nil), data...), nil
	}
	if strings.TrimSpace(key) == "" {
		// Encrypted data with no key cannot be decrypted; fail closed rather
		// than returning the undecorated ciphertext as if it were plaintext.
		return nil, errEmptyKey
	}
	payload, err := base64.StdEncoding.DecodeString(body)
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
	var aad []byte
	if !legacy {
		aad = contextAAD(context)
	}
	return aead.Open(nil, nonce, payload[aead.NonceSize():], aad)
}

// sealWithAAD generates a fresh nonce and returns nonce||ciphertext with aad
// authenticated, so a value sealed for one context cannot be opened for another.
func sealWithAAD(aead cipher.AEAD, plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, aad), nil
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

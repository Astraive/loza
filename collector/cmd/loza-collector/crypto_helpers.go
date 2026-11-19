package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const encryptedBlobPrefix = "enc:"

func encryptionEnabled(key string) bool {
	return strings.TrimSpace(key) != ""
}

func encryptBlob(plain []byte, key string) ([]byte, error) {
	aead, err := buildAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	sealed := aead.Seal(nonce, nonce, plain, nil)
	out := encryptedBlobPrefix + base64.StdEncoding.EncodeToString(sealed)
	return []byte(out), nil
}

// decryptBlob decrypts a blob written by encryptBlob.
//
// When an encryption key is configured, every stored blob is expected to carry
// the encryptedBlobPrefix. Unprefixed data is rejected with an error: returning
// it as plaintext would let anyone able to modify stored blobs downgrade an
// encrypted value to arbitrary plaintext simply by stripping the (unauthenticated)
// prefix. When no key is configured, data is treated as plaintext and returned
// unchanged.
func decryptBlob(data []byte, key string) ([]byte, error) {
	raw := strings.TrimSpace(string(data))
	if !strings.HasPrefix(raw, encryptedBlobPrefix) {
		if encryptionEnabled(key) {
			return nil, fmt.Errorf("stored blob is missing %q prefix while encryption is enabled", encryptedBlobPrefix)
		}
		return data, nil
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, encryptedBlobPrefix))
	if err != nil {
		return nil, err
	}
	aead, err := buildAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(payload) < aead.NonceSize() {
		return nil, fmt.Errorf("encrypted payload too short")
	}
	nonce := payload[:aead.NonceSize()]
	ciphertext := payload[aead.NonceSize():]
	return aead.Open(nil, nonce, ciphertext, nil)
}

func buildAEAD(key string) (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

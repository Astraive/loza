package atrest

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestEncryptStringRejectsEmptyKey(t *testing.T) {
	for _, key := range []string{"", "   ", "\t\n"} {
		got, err := EncryptString([]byte("sensitive event"), key)
		if err == nil {
			t.Fatalf("EncryptString with key %q: expected error, got none (value %q)", key, got)
		}
		if !errors.Is(err, errEmptyKey) {
			t.Fatalf("EncryptString with key %q: expected errEmptyKey, got %v", key, err)
		}
		if got != "" {
			t.Fatalf("EncryptString with key %q: expected no plaintext on error, got %q", key, got)
		}
	}
}

func TestEncryptBytesRejectsEmptyKey(t *testing.T) {
	got, err := EncryptBytes([]byte("sensitive event"), "")
	if err == nil {
		t.Fatalf("EncryptBytes with empty key: expected error, got none (value %q)", got)
	}
	if !errors.Is(err, errEmptyKey) {
		t.Fatalf("EncryptBytes with empty key: expected errEmptyKey, got %v", err)
	}
	if got != nil {
		t.Fatalf("EncryptBytes with empty key: expected nil on error, got %q", got)
	}
}

func TestEncryptStringRoundTrip(t *testing.T) {
	plain := []byte(`{"id":"abc","secret":"value"}`)
	enc, err := EncryptString(plain, "a-valid-key")
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	if !strings.HasPrefix(enc, Prefix) {
		t.Fatalf("ciphertext %q missing prefix %q", enc, Prefix)
	}
	if strings.Contains(enc, "secret") {
		t.Fatalf("ciphertext %q leaked plaintext", enc)
	}
	dec, err := DecryptBytes([]byte(enc), "a-valid-key")
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	if !bytes.Equal(dec, plain) {
		t.Fatalf("round trip mismatch: got %q, want %q", dec, plain)
	}
}

func TestDecryptBytesEncryptedWithEmptyKeyFailsClosed(t *testing.T) {
	enc, err := EncryptString([]byte("sensitive event"), "a-valid-key")
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	got, err := DecryptBytes([]byte(enc), "")
	if err == nil {
		t.Fatalf("DecryptBytes of encrypted data with empty key: expected error, got none (value %q)", got)
	}
	if !errors.Is(err, errEmptyKey) {
		t.Fatalf("DecryptBytes of encrypted data with empty key: expected errEmptyKey, got %v", err)
	}
}

func TestDecryptBytesPlaintextWithEmptyKeyPassesThrough(t *testing.T) {
	plain := []byte("legacy unencrypted event")
	got, err := DecryptBytes(plain, "")
	if err != nil {
		t.Fatalf("DecryptBytes of plaintext with empty key: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("DecryptBytes pass-through mismatch: got %q, want %q", got, plain)
	}
}

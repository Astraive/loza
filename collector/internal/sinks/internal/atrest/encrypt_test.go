package atrest

import (
	"bytes"
	"encoding/base64"
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

func TestDecryptBytesRejectsUnprefixedDataWithKey(t *testing.T) {
	// Regression (MASS-0235): with a key configured, data lacking the "enc:"
	// prefix was returned verbatim and treated as trusted plaintext. That lets
	// anyone with write access to the store inject arbitrary plaintext that
	// bypasses decryption and authentication.
	injected := []byte(`{"event_id":"attacker-controlled"}`)
	got, err := DecryptBytes(injected, "a-storage-encryption-key")
	if err == nil {
		t.Fatalf("expected error for unprefixed data with key, got plaintext %q", got)
	}
	if !errors.Is(err, ErrMissingPrefix) {
		t.Fatalf("expected ErrMissingPrefix, got %v", err)
	}
	if got != nil {
		t.Fatalf("expected no plaintext on error, got %q", got)
	}
}

func TestDecryptBytesRejectsUnprefixedWhitespacePaddedDataWithKey(t *testing.T) {
	// Trimming whitespace must not turn injected data into an accepted value.
	injected := []byte("  plaintext-without-prefix  ")
	if _, err := DecryptBytes(injected, "a-storage-encryption-key"); !errors.Is(err, ErrMissingPrefix) {
		t.Fatalf("expected ErrMissingPrefix, got %v", err)
	}
}

func TestDecryptBytesWrongKeyFails(t *testing.T) {
	enc, err := EncryptString([]byte("secret"), "right-key")
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	if _, err := DecryptBytes([]byte(enc), "wrong-key"); err == nil {
		t.Fatal("expected authentication failure with wrong key")
	}
}

// sealLegacy reproduces the original (vulnerable) behaviour: AEAD.Seal with a
// nil AAD and the legacy "enc:" prefix. It is used to build fixtures that prove
// backward compatibility and to reproduce the context-swap defect.
func sealLegacy(t *testing.T, plain []byte, key string) []byte {
	t.Helper()
	aead, err := buildAEAD(key)
	if err != nil {
		t.Fatalf("buildAEAD: %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	for i := range nonce {
		nonce[i] = byte(i)
	}
	sealed := aead.Seal(nonce, nonce, plain, nil)
	return []byte(Prefix + base64.StdEncoding.EncodeToString(sealed))
}

func TestContextBoundRoundTrip(t *testing.T) {
	key := "test-storage-key"
	plain := []byte(`{"user":"alice","secret":"top"}`)
	context := "postgres/events/raw"

	blob, err := EncryptStringWithContext(plain, key, context)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !bytes.HasPrefix([]byte(blob), []byte(ContextPrefix)) {
		t.Fatalf("expected context-bound prefix %q, got %q", ContextPrefix, blob[:min(len(blob), 8)])
	}
	got, err := DecryptBytesWithContext([]byte(blob), key, context)
	if err != nil {
		t.Fatalf("decrypt with correct context: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, plain)
	}

	// Any other context must fail authentication.
	if _, err := DecryptBytesWithContext([]byte(blob), key, "postgres/audit/raw"); err == nil {
		t.Fatal("expected decryption under a different context to fail")
	}
}

// TestCiphertextSwapDetected is the regression test for the reported finding.
// Before the fix, both values were sealed with nil AAD, so swapping the blobs
// between two fields was undetectable. The first half reproduces that defect
// with the legacy format; the second half proves the context-bound format
// detects the swap.
func TestCiphertextSwapDetected(t *testing.T) {
	const (
		key      = "shared-static-key"
		emailCtx = "users/email"
		ssnCtx   = "users/ssn"
	)
	email := []byte("alice@example.com")
	ssn := []byte("123-45-6789")

	t.Run("legacy nil AAD is interchangeable", func(t *testing.T) {
		emailBlob := sealLegacy(t, email, key)
		ssnBlob := sealLegacy(t, ssn, key)

		// Legacy decryption has no context to check, so a swapped blob is
		// accepted - this is the defect being fixed.
		got, err := DecryptBytesWithContext(emailBlob, key, emailCtx)
		if err != nil {
			t.Fatalf("legacy decrypt: %v", err)
		}
		if !bytes.Equal(got, email) {
			t.Fatalf("legacy round trip mismatch: %q", got)
		}
		got, err = DecryptBytesWithContext(ssnBlob, key, emailCtx)
		if err != nil || !bytes.Equal(got, ssn) {
			t.Fatalf("legacy blob from another field was accepted: got %q, err %v", got, err)
		}
	})

	t.Run("context bound AAD rejects swap", func(t *testing.T) {
		emailBlob, err := EncryptStringWithContext(email, key, emailCtx)
		if err != nil {
			t.Fatalf("encrypt email: %v", err)
		}
		ssnBlob, err := EncryptStringWithContext(ssn, key, ssnCtx)
		if err != nil {
			t.Fatalf("encrypt ssn: %v", err)
		}
		if emailBlob == ssnBlob {
			t.Fatal("distinct contexts/plaintexts produced identical ciphertext")
		}

		// A blob sealed for users/ssn replayed into the users/email field must
		// fail to authenticate.
		if _, err := DecryptBytesWithContext([]byte(ssnBlob), key, emailCtx); err == nil {
			t.Fatal("ciphertext from another field was accepted under this context")
		}
		if _, err := DecryptBytesWithContext([]byte(emailBlob), key, ssnCtx); err == nil {
			t.Fatal("ciphertext from another field was accepted under this context")
		}
	})
}

func TestLegacyCiphertextStillDecrypts(t *testing.T) {
	key := "test-storage-key"
	plain := []byte("data written by the previous version")

	legacy := sealLegacy(t, plain, key)
	got, err := DecryptBytes(legacy, key)
	if err != nil {
		t.Fatalf("legacy ciphertext no longer decrypts: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("legacy round trip mismatch: got %q, want %q", got, plain)
	}
}

func TestDefaultHelpersRoundTrip(t *testing.T) {
	key := "test-storage-key"
	plain := []byte(`{"event_id":"evt-1"}`)

	blob, err := EncryptBytes(plain, key)
	if err != nil {
		t.Fatalf("EncryptBytes: %v", err)
	}
	got, err := DecryptBytes(blob, key)
	if err != nil {
		t.Fatalf("DecryptBytes: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("default helpers round trip mismatch: got %q, want %q", got, plain)
	}
}

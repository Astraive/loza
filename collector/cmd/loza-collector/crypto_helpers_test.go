package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const testBlobEncryptionKey = "regression-test-encryption-key"

func TestDecryptBlobRejectsUnprefixedDataWhenEncryptionEnabled(t *testing.T) {
	// An attacker who can modify stored blobs can downgrade an encrypted value
	// to arbitrary plaintext by stripping the (unauthenticated) "enc:" prefix.
	// With a key configured, decryptBlob must reject unprefixed data instead of
	// silently returning it as legitimate plaintext.
	plaintext := []byte(`{"event_id":"attacker-controlled","event":"downgraded"}`)
	got, err := decryptBlob(plaintext, testBlobEncryptionKey)
	if err == nil {
		t.Fatalf("decryptBlob accepted unprefixed plaintext with encryption enabled: got %q", got)
	}
	if got != nil {
		t.Fatalf("decryptBlob returned data on rejection: got %q", got)
	}
}

func TestDecryptBlobRoundTripWithEncryptionEnabled(t *testing.T) {
	plaintext := []byte(`{"event_id":"1","event":"ok"}`)
	sealed, err := encryptBlob(plaintext, testBlobEncryptionKey)
	if err != nil {
		t.Fatalf("encryptBlob: %v", err)
	}
	if !bytes.HasPrefix(sealed, []byte(encryptedBlobPrefix)) {
		t.Fatalf("expected %q prefix, got %q", encryptedBlobPrefix, sealed)
	}
	got, err := decryptBlob(sealed, testBlobEncryptionKey)
	if err != nil {
		t.Fatalf("decryptBlob: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, plaintext)
	}
}

func TestDecryptBlobPassesThroughPlaintextWhenEncryptionDisabled(t *testing.T) {
	plaintext := []byte(`{"event_id":"1","event":"plaintext"}`)
	got, err := decryptBlob(plaintext, "")
	if err != nil {
		t.Fatalf("decryptBlob with empty key: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("unexpected plaintext passthrough: got %q, want %q", got, plaintext)
	}
}

func TestLoadPersistedSchemaRegistryRejectsPlaintextWhenEncrypted(t *testing.T) {
	// A registry written without encryption must not be accepted when an
	// encryption key is configured (downgrade protection at the caller).
	path := filepath.Join(t.TempDir(), "registry.json")
	plaintext := []byte(`[{"SchemaVersion":"v1"}]`)
	if err := os.WriteFile(path, plaintext, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	if _, err := loadPersistedSchemaRegistry(path, testBlobEncryptionKey); err == nil {
		t.Fatal("loadPersistedSchemaRegistry accepted plaintext registry while encryption key configured")
	}
}

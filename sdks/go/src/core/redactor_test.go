package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactKeysKeepsNonMatchingFields(t *testing.T) {
	attrs := []Attr{
		String("token", "abc"),
		String("user.id", "u_123"),
	}

	out := applyRedactor(attrs, RedactKeys("token"))
	if len(out) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(out))
	}
	if out[0].Value != redactedValue {
		t.Fatalf("expected token to be redacted, got %v", out[0].Value)
	}
	if out[1].Key != "user.id" || out[1].Value != "u_123" {
		t.Fatalf("unexpected non-matching attr mutation: %+v", out[1])
	}
}

func TestDropKeysDropsOnlyMatchingFields(t *testing.T) {
	attrs := []Attr{
		String("password", "secret"),
		String("user.id", "u_123"),
	}

	out := applyRedactor(attrs, DropKeys("password"))
	if len(out) != 1 {
		t.Fatalf("expected 1 attr after drop, got %d", len(out))
	}
	if out[0].Key != "user.id" {
		t.Fatalf("expected user.id to remain, got %s", out[0].Key)
	}
}

func TestComposeRedactorsKeepsUnmatched(t *testing.T) {
	attrs := []Attr{
		String("user.email", "a@example.com"),
		String("cart.id", "c_1"),
	}

	out := applyRedactor(attrs, ComposeRedactors(HashKeys("user.email"), DropKeys("secret")))
	if len(out) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(out))
	}
	if out[0].Value == "a@example.com" {
		t.Fatalf("expected user.email to be hashed")
	}
	if out[1].Key != "cart.id" || out[1].Value != "c_1" {
		t.Fatalf("expected unmatched attr unchanged, got %+v", out[1])
	}
}

func TestDefaultRedactorRedactsSafetyNetKeys(t *testing.T) {
	attrs := []Attr{
		String("password", "hunter2"),
		String("api_key", "sk-123"),
		String("authorization", "Bearer xyz"),
		String("user.id", "u_123"),
		String("service", "checkout"),
	}

	out := applyRedactor(attrs, DefaultRedactor())
	if len(out) != 5 {
		t.Fatalf("expected 5 attrs, got %d", len(out))
	}
	// Safety-net keys should be redacted
	if out[0].Value != redactedValue {
		t.Fatalf("expected password to be redacted, got %v", out[0].Value)
	}
	if out[1].Value != redactedValue {
		t.Fatalf("expected api_key to be redacted, got %v", out[1].Value)
	}
	if out[2].Value != redactedValue {
		t.Fatalf("expected authorization to be redacted, got %v", out[2].Value)
	}
	// Non-sensitive keys should be untouched
	if out[3].Value != "u_123" {
		t.Fatalf("expected user.id to be unchanged, got %v", out[3].Value)
	}
	if out[4].Value != "checkout" {
		t.Fatalf("expected service to be unchanged, got %v", out[4].Value)
	}
}

func TestDefaultRedactorRedactsNestedKeys(t *testing.T) {
	attrs := []Attr{
		Group("user", String("secret", "top-secret")),
	}

	out := applyRedactor(attrs, DefaultRedactor())
	children, ok := out[0].Value.([]Attr)
	if !ok || len(children) != 1 {
		t.Fatalf("expected nested group attrs, got %+v", out[0].Value)
	}
	if children[0].Value != redactedValue {
		t.Fatalf("expected nested secret to be redacted, got %v", children[0].Value)
	}
}

func TestDefaultRedactorDoesNotRedactPIIValues(t *testing.T) {
	// SDK safety-net redactor only redacts KEYS, not PII values in strings.
	// The collector owns PII value redaction (email, phone, SSN, etc.).
	attrs := []Attr{
		String("note", "contact alice@example.com"),
		String("user.email", "alice@example.com"),
	}

	out := applyRedactor(attrs, DefaultRedactor())
	// "note" is not a safety-net key — value should be unchanged
	if out[0].Value != "contact alice@example.com" {
		t.Fatalf("expected note value unchanged, got %v", out[0].Value)
	}
	// "user.email" is not a safety-net key — value should be unchanged
	if out[1].Value != "alice@example.com" {
		t.Fatalf("expected user.email value unchanged (collector handles PII), got %v", out[1].Value)
	}
}

func TestApplyRedactorDoesNotMutateInput(t *testing.T) {
	attrs := []Attr{
		Group("user", String("password", "secret")),
		String("note", "hello"),
	}
	before := cloneAttrs(attrs)

	out := applyRedactor(attrs, DefaultRedactor())
	if !reflect.DeepEqual(attrs, before) {
		t.Fatalf("expected input attrs to remain unchanged: before=%+v after=%+v", before, attrs)
	}
	if reflect.DeepEqual(out, attrs) {
		t.Fatalf("expected redacted output to differ from input")
	}
}

func TestMaskKeysPartiallyMasksValues(t *testing.T) {
	attrs := []Attr{
		String("credit_card", "4111111111111111"),
		String("user.id", "u_123"),
	}

	out := applyRedactor(attrs, MaskKeys("credit_card"))
	if out[0].Value == "4111111111111111" {
		t.Fatalf("expected credit_card to be masked")
	}
	s, ok := out[0].Value.(string)
	if !ok {
		t.Fatalf("expected masked value to be string, got %T", out[0].Value)
	}
	if !strings.Contains(s, "*") {
		t.Fatalf("expected masked value to contain asterisks, got %s", s)
	}
	if out[1].Value != "u_123" {
		t.Fatalf("expected user.id unchanged, got %v", out[1].Value)
	}
}

func TestMaskKeysPreservesMultiByteUTF8(t *testing.T) {
	// Each CJK rune is 3 bytes in UTF-8. Slicing by byte index cuts mid-rune
	// and emits invalid UTF-8, so masking must be done by rune index.
	const value = "日本語テスト"
	attrs := []Attr{String("name", value)}

	out := applyRedactor(attrs, MaskKeys("name"))
	s, ok := out[0].Value.(string)
	if !ok {
		t.Fatalf("expected masked value to be string, got %T", out[0].Value)
	}
	if !utf8.ValidString(s) {
		t.Fatalf("masked value is not valid UTF-8: %q", s)
	}

	runes := []rune(value)
	want := string(runes[:2]) + strings.Repeat("*", len(runes)-4) + string(runes[len(runes)-2:])
	if s != want {
		t.Fatalf("expected masked value %q, got %q", want, s)
	}
}

func TestMaskKeysShortMultiByteUTF8(t *testing.T) {
	// "日本語" is 3 runes but 9 bytes. The byte-based length check treated it
	// as long enough to mask, producing a mid-rune cut; 3 runes must map to
	// the fully-masked "****" case like any other short value.
	attrs := []Attr{String("name", "日本語")}

	out := applyRedactor(attrs, MaskKeys("name"))
	s, ok := out[0].Value.(string)
	if !ok {
		t.Fatalf("expected masked value to be string, got %T", out[0].Value)
	}
	if s != "****" {
		t.Fatalf("expected fully masked value for short multi-byte input, got %q", s)
	}
	if !utf8.ValidString(s) {
		t.Fatalf("masked value is not valid UTF-8: %q", s)
	}
}

func TestHashKeysHashesValues(t *testing.T) {
	attrs := []Attr{
		String("user.email", "alice@example.com"),
		String("user.id", "u_123"),
	}

	out := applyRedactor(attrs, HashKeys("user.email"))
	if out[0].Value == "alice@example.com" {
		t.Fatalf("expected user.email to be hashed")
	}
	if out[1].Value != "u_123" {
		t.Fatalf("expected user.id unchanged, got %v", out[1].Value)
	}
}

// Regression test for MASS-0442: a bare SHA-256 digest of a low-entropy value
// is reversible via dictionary/rainbow lookup, so the redactor must use a
// keyed MAC instead.
func TestHashKeysUsesKeyedMACNotBareSHA256(t *testing.T) {
	const plaintext = "alice@example.com"

	bare := sha256.Sum256([]byte(plaintext))
	bareHex := fmt.Sprintf("%x", bare)

	got, keep := HashKeys("user.email").Redact("user.email", plaintext)
	if !keep {
		t.Fatal("expected matching key to be kept")
	}
	gotHex, ok := got.(string)
	if !ok {
		t.Fatalf("expected string digest, got %T", got)
	}
	if gotHex == plaintext {
		t.Fatal("hashed value leaked the plaintext")
	}
	if gotHex == bareHex {
		t.Fatalf("value hashed with unkeyed SHA-256 (%s); dictionary recovery is possible", bareHex)
	}

	mac := hmac.New(sha256.New, ephemeralHashKey)
	_, _ = mac.Write([]byte(plaintext))
	wantHex := fmt.Sprintf("%x", mac.Sum(nil))
	if gotHex != wantHex {
		t.Fatalf("digest = %q, want HMAC-SHA256 = %q", gotHex, wantHex)
	}
}

func TestHashKeysWithSecretIsDeterministicAndKeyDependent(t *testing.T) {
	const plaintext = "123-45-6789"

	first, keep := HashKeysWithSecret([]byte("stable-secret"), "ssn").Redact("ssn", plaintext)
	if !keep {
		t.Fatal("expected matching key to be kept")
	}
	second, _ := HashKeysWithSecret([]byte("stable-secret"), "ssn").Redact("ssn", plaintext)
	other, _ := HashKeysWithSecret([]byte("different-secret"), "ssn").Redact("ssn", plaintext)

	if first != second {
		t.Fatalf("same secret produced different digests: %q vs %q", first, second)
	}
	if first == other {
		t.Fatal("different secrets produced the same digest")
	}
	bare := sha256.Sum256([]byte(plaintext))
	if first == fmt.Sprintf("%x", bare) {
		t.Fatal("explicit-secret hashing fell back to bare SHA-256")
	}
	mac := hmac.New(sha256.New, []byte("stable-secret"))
	_, _ = mac.Write([]byte(plaintext))
	if want := fmt.Sprintf("%x", mac.Sum(nil)); first != want {
		t.Fatalf("digest = %q, want HMAC-SHA256 = %q", first, want)
	}
}

func TestHashKeysWithEmptySecretUsesEphemeralKey(t *testing.T) {
	const plaintext = "u_123"
	got, _ := HashKeysWithSecret(nil, "user.id").Redact("user.id", plaintext)
	want, _ := HashKeys("user.id").Redact("user.id", plaintext)
	if got != want {
		t.Fatalf("empty secret digest = %q, want ephemeral digest %q", got, want)
	}
}

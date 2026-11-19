package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

// Redactor scrubs sensitive values from an event before encoding.
type Redactor interface {
	// Redact examines key and value. It returns the (possibly modified) value
	// and whether to keep the field. Return keep=false to drop the field entirely.
	Redact(key string, value any) (newValue any, keep bool)
}

const redactedValue = "[REDACTED]"

type noRedactionMarker struct{}

var noRedaction = &noRedactionMarker{}

// defaultSensitiveKeys is the safety-net list of obviously sensitive keys.
// The collector owns the real PII policy (email, phone, SSN, IP, regex, etc.).
var defaultSensitiveKeys = []string{
	"password", "passwd", "pwd", "secret", "token", "access_token",
	"refresh_token", "api_key", "apikey", "auth", "authorization",
	"credential", "private_key", "client_secret",
}

// keyRedactor replaces the value of matching keys with [REDACTED].
type keyRedactor struct{ keys map[string]struct{} }

func (r *keyRedactor) Redact(key string, _ any) (any, bool) {
	lower := strings.ToLower(key)
	if _, ok := r.keys[lower]; ok {
		return redactedValue, true
	}
	return noRedaction, true
}

// DefaultRedactor returns a Redactor that replaces common sensitive keys.
func DefaultRedactor() Redactor {
	return RedactKeys(defaultSensitiveKeys...)
}

// RedactKeys returns a Redactor that replaces values for the given keys.
func RedactKeys(keys ...string) Redactor {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[strings.ToLower(k)] = struct{}{}
	}
	return &keyRedactor{keys: m}
}

// ephemeralHashKey is the process-wide secret used by HashKeys when no explicit
// secret is supplied. A keyed MAC is required here: a bare SHA-256 digest of a
// low-entropy value (email, SSN, user ID) can be reversed with a dictionary or
// rainbow table, exposing the original PII to anyone holding the logs.
var ephemeralHashKey = newEphemeralHashKey()

func newEphemeralHashKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("core: cannot generate redaction hash key: " + err.Error())
	}
	return key
}

// hashRedactor replaces values of matching keys with a keyed HMAC-SHA256 digest.
type hashRedactor struct {
	keys map[string]struct{}
	key  []byte
}

func (r *hashRedactor) Redact(key string, value any) (any, bool) {
	lower := strings.ToLower(key)
	if _, ok := r.keys[lower]; !ok {
		return noRedaction, true
	}
	mac := hmac.New(sha256.New, r.key)
	_, _ = mac.Write([]byte(fmt.Sprintf("%v", value)))
	return fmt.Sprintf("%x", mac.Sum(nil)), true
}

// HashKeys returns a Redactor that replaces values for the given keys with a
// keyed HMAC-SHA256 digest. The MAC key is random per process, so digests are
// stable within a running process but are not comparable across restarts or
// replicas. Use HashKeysWithSecret when a stable cross-process pseudonym is
// required.
func HashKeys(keys ...string) Redactor {
	return HashKeysWithSecret(ephemeralHashKey, keys...)
}

// HashKeysWithSecret returns a Redactor like HashKeys that uses secret as the
// MAC key. Reusing the same secret produces the same digest for a given value,
// while an attacker without the secret cannot dictionary-attack the output.
// An empty secret falls back to the per-process ephemeral key.
func HashKeysWithSecret(secret []byte, keys ...string) Redactor {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[strings.ToLower(k)] = struct{}{}
	}
	key := ephemeralHashKey
	if len(secret) > 0 {
		key = append([]byte(nil), secret...)
	}
	return &hashRedactor{keys: m, key: key}
}

// maskRedactor partially masks values for configured keys.
type maskRedactor struct{ keys map[string]struct{} }

func (r *maskRedactor) Redact(key string, value any) (any, bool) {
	lower := strings.ToLower(key)
	if _, ok := r.keys[lower]; !ok {
		return noRedaction, true
	}
	runes := []rune(fmt.Sprintf("%v", value))
	if len(runes) <= 4 {
		return "****", true
	}
	return string(runes[:2]) + strings.Repeat("*", len(runes)-4) + string(runes[len(runes)-2:]), true
}

// MaskKeys returns a Redactor that partially masks values for given keys.
func MaskKeys(keys ...string) Redactor {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[strings.ToLower(k)] = struct{}{}
	}
	return &maskRedactor{keys: m}
}

// dropRedactor drops fields with matching keys entirely.
type dropRedactor struct{ keys map[string]struct{} }

func (r *dropRedactor) Redact(key string, _ any) (any, bool) {
	lower := strings.ToLower(key)
	if _, ok := r.keys[lower]; ok {
		return nil, false // keep=false drops the field
	}
	return noRedaction, true
}

// DropKeys returns a Redactor that removes fields with the given keys.
func DropKeys(keys ...string) Redactor {
	m := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		m[strings.ToLower(k)] = struct{}{}
	}
	return &dropRedactor{keys: m}
}

// compositeRedactor applies multiple Redactors in order, stopping at first match.
type compositeRedactor struct{ redactors []Redactor }

func (c *compositeRedactor) Redact(key string, value any) (any, bool) {
	for _, r := range c.redactors {
		newVal, keep := r.Redact(key, value)
		if !keep {
			return nil, false
		}
		if newVal != noRedaction {
			return newVal, true
		}
	}
	return value, true
}

// ComposeRedactors combines multiple redactors; the first match wins.
func ComposeRedactors(redactors ...Redactor) Redactor {
	filtered := make([]Redactor, 0, len(redactors))
	for _, r := range redactors {
		if r != nil {
			filtered = append(filtered, r)
		}
	}
	return &compositeRedactor{redactors: filtered}
}

// applyRedactor walks event attrs and applies the redactor to a cloned payload.
func applyRedactor(attrs []Attr, r Redactor) []Attr {
	if r == nil {
		return attrs
	}
	out := cloneAttrs(attrs)
	dst := out[:0]
	for i := range out {
		a := out[i]
		if a.Kind == KindGroup {
			if children, ok := a.Value.([]Attr); ok {
				a.Value = applyRedactor(children, r)
			}
			dst = append(dst, a)
			continue
		}
		newVal, keep := r.Redact(a.Key, a.Value)
		if !keep {
			continue
		}
		if newVal != noRedaction {
			a.Value = newVal
			if _, isStr := newVal.(string); isStr {
				a.Kind = KindString
			}
		}
		dst = append(dst, a)
	}
	return dst
}

// patternRedactor matches keys against regex patterns and replaces values.
type patternRedactor struct {
	patterns []*regexp.Regexp
}

func (r *patternRedactor) Redact(key string, _ any) (any, bool) {
	for _, p := range r.patterns {
		if p.MatchString(strings.ToLower(key)) {
			return redactedValue, true
		}
	}
	return noRedaction, true
}

// RedactPatterns returns a Redactor that replaces values for keys matching
// any of the given regex patterns (case-insensitive).
func RedactPatterns(patterns ...string) Redactor {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		compiled = append(compiled, regexp.MustCompile(`(?i)`+p))
	}
	return &patternRedactor{patterns: compiled}
}

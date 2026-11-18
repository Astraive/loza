package redaction

import (
	"strings"
	"testing"
)

// TestRedactMapFailsClosedForEmptyAndUnknownModes reproduces MASS-0354: a
// zero-value/unknown mode must never disable redaction.
func TestRedactMapFailsClosedForEmptyAndUnknownModes(t *testing.T) {
	cases := []struct {
		name string
		r    *Redactor
	}{
		{"New with empty mode", New("")},
		{"NewStrict with empty mode", NewStrict("")},
		{"NewWithConfig with zero-value config", NewWithConfig(Config{})},
		{"NewWithConfig with unknown mode", NewWithConfig(Config{Mode: Mode("bogus")})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]interface{}{
				"message":  "contact alice@example.com today",
				"password": "hunter2",
			}

			output := tc.r.RedactMap(input)

			if got, _ := output["message"].(string); strings.Contains(got, "alice@example.com") {
				t.Errorf("email was left unredacted: %q", got)
			}
			if got, _ := output["password"].(string); got != "[REDACTED]" {
				t.Errorf("sensitive field was left unredacted: %q", got)
			}
		})
	}
}

// TestNewWithConfigUnknownModeUsesEnforcePatterns verifies an unknown mode gets
// the full enforce pattern set, including IPs, rather than degrading to
// pass-through.
func TestNewWithConfigUnknownModeUsesEnforcePatterns(t *testing.T) {
	r := NewWithConfig(Config{Mode: Mode("not-a-mode")})

	output := r.RedactMap(map[string]interface{}{"message": "request came from 10.0.0.1"})

	if got, _ := output["message"].(string); strings.Contains(got, "10.0.0.1") {
		t.Errorf("IP was left unredacted: %q", got)
	}
}

// TestConstructorsNormalizeMode pins the fail-closed normalization at
// construction time so callers cannot obtain a bypassing redactor.
func TestConstructorsNormalizeMode(t *testing.T) {
	cases := []struct {
		name string
		r    *Redactor
		want Mode
	}{
		{"New empty", New(""), ModeEnforce},
		{"NewStrict unknown", NewStrict("bogus"), ModeEnforce},
		{"NewWithConfig zero", NewWithConfig(Config{}), ModeEnforce},
		{"NewWithConfig log", NewWithConfig(Config{Mode: ModeLog}), ModeLog},
		{"New enforce", New(ModeEnforce), ModeEnforce},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.r.mode != tc.want {
				t.Errorf("mode = %q, want %q", tc.r.mode, tc.want)
			}
		})
	}
}

// TestModeLogRemainsObserveOnly ensures the explicit log mode keeps its
// documented detect-and-log (no mutation) behavior.
func TestModeLogRemainsObserveOnly(t *testing.T) {
	r := NewWithConfig(Config{Mode: ModeLog})
	input := map[string]interface{}{"message": "contact bob@example.com"}

	output := r.RedactMap(input)

	if got, _ := output["message"].(string); got != "contact bob@example.com" {
		t.Errorf("log mode should not mutate values, got %q", got)
	}
}

// TestRedactMapZeroValueAndNilReceiver checks defensive behavior for a
// manually constructed zero-value Redactor and a nil receiver.
func TestRedactMapZeroValueAndNilReceiver(t *testing.T) {
	input := map[string]interface{}{"password": "hunter2"}

	if got, _ := (&Redactor{}).RedactMap(input)["password"].(string); got != "[REDACTED]" {
		t.Errorf("zero-value redactor left sensitive field unredacted: %q", got)
	}

	var nilRedactor *Redactor
	if got := nilRedactor.RedactMap(input); got == nil {
		t.Error("nil receiver should return the input map")
	}
}

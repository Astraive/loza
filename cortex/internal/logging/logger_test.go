package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    zerolog.Level
		wantErr bool
	}{
		{name: "debug", input: "debug", want: zerolog.DebugLevel},
		{name: "info", input: "info", want: zerolog.InfoLevel},
		{name: "warn", input: "warn", want: zerolog.WarnLevel},
		{name: "error", input: "error", want: zerolog.ErrorLevel},
		{name: "case insensitive", input: "DEBUG", want: zerolog.DebugLevel},
		{name: "surrounding whitespace", input: " warn ", want: zerolog.WarnLevel},
		{name: "unknown level returns error", input: "unknown", want: zerolog.InfoLevel, wantErr: true},
		{name: "typo returns error", input: "warnd", want: zerolog.InfoLevel, wantErr: true},
		{name: "empty level returns error", input: "", want: zerolog.InfoLevel, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLevel(tt.input)
			if got != tt.want {
				t.Fatalf("parseLevel(%q) level = %v, want %v", tt.input, got, tt.want)
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestParseLevelUnknownErrorMentionsValue(t *testing.T) {
	_, err := parseLevel("bogus")
	if err == nil {
		t.Fatal("expected an error for an unknown level")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error to name the invalid level, got %q", err)
	}
}

func TestNewReturnsLogger(t *testing.T) {
	if got := New("info", "json"); got == nil {
		t.Fatal("expected logger")
	}
}

func TestNewDoesNotMutateGlobalLevel(t *testing.T) {
	// Establish a known global level and always restore it so this test stays
	// isolated from any other test that touches zerolog's global state.
	original := zerolog.GlobalLevel()
	t.Cleanup(func() { zerolog.SetGlobalLevel(original) })

	const sentinel = zerolog.ErrorLevel
	zerolog.SetGlobalLevel(sentinel)

	// Constructing a Logger whose level differs from the global level must not
	// overwrite the process-wide setting.
	_ = New("debug", "json")

	if got := zerolog.GlobalLevel(); got != sentinel {
		t.Fatalf("New() changed the global zerolog level: got %v, want %v", got, sentinel)
	}
}

func TestNewAppliesLevelPerInstance(t *testing.T) {
	debug := New("debug", "json").GetZerologLogger().GetLevel()
	if debug != zerolog.DebugLevel {
		t.Fatalf("debug logger level = %v, want %v", debug, zerolog.DebugLevel)
	}

	warn := New("warn", "json").GetZerologLogger().GetLevel()
	if warn != zerolog.WarnLevel {
		t.Fatalf("warn logger level = %v, want %v", warn, zerolog.WarnLevel)
	}
}

func TestNewLevelsAreIndependent(t *testing.T) {
	// Pin the global level so it cannot filter the messages under test; this
	// mirrors a host application that configures zerolog explicitly at init.
	original := zerolog.GlobalLevel()
	t.Cleanup(func() { zerolog.SetGlobalLevel(original) })
	zerolog.SetGlobalLevel(zerolog.TraceLevel)

	var debugOut, errorOut bytes.Buffer
	debug := New("debug", "json").GetZerologLogger().Output(&debugOut)
	errorOnly := New("error", "json").GetZerologLogger().Output(&errorOut)

	debug.Debug().Msg("visible-debug")
	errorOnly.Debug().Msg("hidden-debug")
	errorOnly.Error().Msg("visible-error")

	if !strings.Contains(debugOut.String(), "visible-debug") {
		t.Fatalf("debug logger suppressed debug message: %q", debugOut.String())
	}
	if strings.Contains(errorOut.String(), "hidden-debug") {
		t.Fatalf("error logger emitted a debug message: %q", errorOut.String())
	}
	if !strings.Contains(errorOut.String(), "visible-error") {
		t.Fatalf("error logger suppressed error message: %q", errorOut.String())
	}
}

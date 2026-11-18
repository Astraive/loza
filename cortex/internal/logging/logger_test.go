package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestParseLevel(t *testing.T) {
	if got := parseLevel("debug"); got != zerolog.DebugLevel {
		t.Fatalf("expected debug level, got %v", got)
	}
	if got := parseLevel("unknown"); got != zerolog.InfoLevel {
		t.Fatalf("expected info fallback, got %v", got)
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

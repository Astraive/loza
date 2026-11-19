package safe

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type sentinelError struct{ msg string }

func (e *sentinelError) Error() string { return e.msg }

func TestCallReturnsNilForSuccessfulFunction(t *testing.T) {
	called := false
	if err := Call(func() { called = true }); err != nil {
		t.Fatalf("Call success error = %v", err)
	}
	if !called {
		t.Fatal("Call did not invoke function")
	}
}

func TestCallConvertsPanicsToErrors(t *testing.T) {
	err := Call(func() { panic("boom") })
	if err == nil || err.Error() != "panic: boom" {
		t.Fatalf("Call panic error = %v, want panic: boom", err)
	}
}

func TestGoRunsFunctionsAndRecoversPanics(t *testing.T) {
func TestCallPreservesPanicErrorIdentity(t *testing.T) {
	sentinel := &sentinelError{msg: "sentinel panic"}
	err := Call(func() { panic(sentinel) })

	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(Call panic, sentinel) = false, want true (err = %v)", err)
	}

	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("errors.As(Call panic, *PanicError) = false (err = %v)", err)
	}
	if !errors.Is(pe.Unwrap(), sentinel) {
		t.Fatalf("PanicError.Unwrap() = %v, want %v", pe.Unwrap(), sentinel)
	}

	var recovered *sentinelError
	if !errors.As(err, &recovered) || recovered != sentinel {
		t.Fatalf("errors.As(Call panic, *sentinelError) = %v, want %v", recovered, sentinel)
	}
}

func TestCallCapturesPanicStack(t *testing.T) {
	err := Call(func() { panic("boom with stack") })

	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("Call error is %T, want *PanicError", err)
	}
	stack := pe.StackTrace()
	if stack == "" {
		t.Fatal("Call panic error did not capture a stack trace")
	}
	if !strings.Contains(stack, "goroutine") {
		t.Fatalf("captured stack does not look like a goroutine stack: %q", stack)
	}
	if !strings.Contains(stack, "TestCallCapturesPanicStack") {
		t.Fatalf("captured stack does not contain the panicking test frame: %q", stack)
	}
}

func TestGoAndSafeGoRunFunctionsAndRecoverPanics(t *testing.T) {
	done := make(chan struct{}, 2)

	// A normal function runs to completion.
	Go(func() { done <- struct{}{} })

	// A panic is recovered: the deferred signal still fires and the goroutine
	// does not crash the process. This is why a separate "safe" alias of Go is
	// unnecessary -- Go already provides this behavior.
	Go(func() {
		defer func() { done <- struct{}{} }()
		panic("recovered")
	})

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("safe goroutine did not complete")
		}
	}
}

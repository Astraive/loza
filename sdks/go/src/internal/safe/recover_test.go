package safe

import (
	"testing"
	"time"
)

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

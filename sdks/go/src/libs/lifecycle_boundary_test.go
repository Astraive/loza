package libs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/astraive/loza/sdks/go/src/libs"
)

func TestFlushAllAndCloseAllSkipNilAndReturnNilOnSuccess(t *testing.T) {
	ctx := context.Background()
	if err := libs.FlushAll(ctx, nil, testFlusher{}); err != nil {
		t.Fatalf("FlushAll success = %v", err)
	}
	if err := libs.CloseAll(ctx, nil, testCloser{}); err != nil {
		t.Fatalf("CloseAll success = %v", err)
	}
	if err := libs.FlushAll(ctx); err != nil {
		t.Fatalf("FlushAll empty = %v", err)
	}
	if err := libs.CloseAll(ctx); err != nil {
		t.Fatalf("CloseAll empty = %v", err)
	}
}

// pointerFlusher and pointerCloser deliberately dereference their receiver so
// that calling the method on a nil *pointerFlusher or nil *pointerCloser panics.
// This reproduces the typed-nil case that a plain `x == nil` check misses.
type pointerFlusher struct{ err error }

func (f *pointerFlusher) Flush(context.Context) error { return f.err }

type pointerCloser struct{ err error }

func (c *pointerCloser) Close(context.Context) error { return c.err }

func TestFlushAllAndCloseAllSkipTypedNil(t *testing.T) {
	ctx := context.Background()

	var nilFlusher *pointerFlusher
	var nilCloser *pointerCloser

	// A typed nil passed on its own must not panic and must be treated as a no-op.
	// CloseAll is the reported defect (lifecycle.go:36); FlushAll has the same pattern.
	if err := libs.CloseAll(ctx, nilCloser); err != nil {
		t.Fatalf("CloseAll(typed nil) = %v, want nil", err)
	}
	if err := libs.FlushAll(ctx, nilFlusher); err != nil {
		t.Fatalf("FlushAll(typed nil) = %v, want nil", err)
	}

	// Typed nils mixed with real implementations must be skipped while the
	// remaining errors are still reported.
	closeErr := errors.New("close failed")
	if err := libs.CloseAll(ctx, nilCloser, &pointerCloser{err: closeErr}, nil, nilCloser); !errors.Is(err, closeErr) {
		t.Fatalf("CloseAll(typed nil + real) = %v, want %v", err, closeErr)
	}

	// A nil interface value is still skipped as before.
	if err := libs.CloseAll(ctx, nil, nilCloser); err != nil {
		t.Fatalf("CloseAll(nil + typed nil) = %v, want nil", err)
	}

	flushErr := errors.New("flush failed")
	if err := libs.FlushAll(ctx, nilFlusher, &pointerFlusher{err: flushErr}, nilFlusher); !errors.Is(err, flushErr) {
		t.Fatalf("FlushAll(typed nil + real) = %v, want %v", err, flushErr)
	}
}

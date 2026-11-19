package safe

import (
	"fmt"
	"os"
	"runtime/debug"
)

// Go runs fn in a goroutine and recovers any panic, writing it to stderr.
func Go(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "[loza] panic recovered: %v\n%s\n", r, debug.Stack())
			}
		}()
		fn()
	}()
}

// SafeGo runs fn in a goroutine and recovers panics.
func SafeGo(fn func()) {
	Go(fn)
}

// PanicError is returned by Call when fn panics. It preserves both the
// recovered panic value (and its error identity, when it implements error)
// and the stack captured at the point of recovery.
type PanicError struct {
	Value interface{} // the value passed to panic
	Stack []byte      // stack trace captured when the panic was recovered
}

// Error implements error while preserving the historical "panic: <value>"
// message.
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.Value)
}

// Unwrap exposes the recovered panic value when it implements error, so
// callers can use errors.Is and errors.As on the returned error.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Value.(error); ok {
		return err
	}
	return nil
}

// StackTrace returns the captured panic stack as a string.
func (e *PanicError) StackTrace() string {
	return string(e.Stack)
}

// Call runs fn and recovers any panic, returning it as an error.
func Call(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	fn()
	return nil
}

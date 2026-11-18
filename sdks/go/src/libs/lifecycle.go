package libs

import (
	"context"
	"errors"
	"reflect"
)

// Flusher is implemented by components that support explicit flush.
type Flusher interface {
	Flush(context.Context) error
}

// Closer is implemented by components that support shutdown/close.
type Closer interface {
	Close(context.Context) error
}

// isNilValue reports whether v is nil, including when v is a non-nil interface
// holding a typed nil value (for example, a nil *T stored in a Closer). A plain
// `v == nil` check misses those values, and invoking a method on the resulting
// nil receiver can panic.
func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// FlushAll flushes all flushers and returns a joined error when one or more fail.
func FlushAll(ctx context.Context, flushers ...Flusher) error {
	var errs []error
	for _, f := range flushers {
		if isNilValue(f) {
			continue
		}
		if err := f.Flush(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CloseAll closes all closers and returns a joined error when one or more fail.
func CloseAll(ctx context.Context, closers ...Closer) error {
	var errs []error
	for _, c := range closers {
		if isNilValue(c) {
			continue
		}
		if err := c.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

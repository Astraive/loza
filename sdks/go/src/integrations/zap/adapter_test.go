package zap

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	loza "github.com/astraive/loza/sdks/go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestAdapterCoreWritesAllLevelsToWrappedCoreAndLoza(t *testing.T) {
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })
	var output bytes.Buffer
	base := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zapcore.DebugLevel)
	adapter := Core(base)
	if adapter == nil || NewCore(base) == nil {
		t.Fatal("Core constructor returned nil")
	}
	for _, level := range []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel} {
		ent := zapcore.Entry{Level: level, Message: "zap message", Time: time.Unix(1, 0)}
		if err := adapter.Write(ent, []zapcore.Field{zap.String("key", "value")}); err != nil {
			t.Fatalf("Write %v: %v", level, err)
		}
	}
	if output.Len() == 0 {
		t.Fatal("wrapped core received no output")
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 4 {
		t.Fatalf("loza events = %d, want 4", store.Len())
	}
}

// configureMirroringTest sets up an isolated loza sink and a zap logger backed
// by AdapterCore. The returned buffer captures what the wrapped core writes.
func configureMirroringTest(t *testing.T) (*zap.Logger, *loza.MemorySinkStore, *bytes.Buffer) {
	t.Helper()
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })
	var output bytes.Buffer
	base := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zapcore.DebugLevel)
	return zap.New(Core(base)), store, &output
}

// Regression: logging through zap.New(adapter) must mirror into loza. Calling
// adapter.Write directly (as older tests did) hid that Check was promoted from
// the embedded core, so the base core was added to the CheckedEntry and
// AdapterCore.Write was never invoked.
func TestAdapterCoreMirrorsLogsThroughZapLogger(t *testing.T) {
	logger, store, output := configureMirroringTest(t)

	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	if output.Len() == 0 {
		t.Fatal("wrapped core received no output")
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 4 {
		t.Fatalf("loza events = %d, want 4", store.Len())
	}
}

// Regression: Logger.With calls Core.With, which used to return a bare clone
// of the base core and lose mirroring on the derived logger.
func TestAdapterCoreWithPreservesMirroring(t *testing.T) {
	logger, store, output := configureMirroringTest(t)

	logger.With(zap.String("component", "test")).Info("with message")

	if output.Len() == 0 {
		t.Fatal("wrapped core received no output")
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("loza events = %d, want 1", store.Len())
	}
}

// Regression: Logger.Named shares the core, so mirroring must survive naming.
func TestAdapterCoreNamedPreservesMirroring(t *testing.T) {
	logger, store, output := configureMirroringTest(t)

	logger.Named("worker").Info("named message")

	if output.Len() == 0 {
		t.Fatal("wrapped core received no output")
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("loza events = %d, want 1", store.Len())
	}
}

// Regression test for MASS-0478: structured fields (and caller/stack) must be
// mirrored into loza rather than dropped when only the wrapped core sees them.
func TestAdapterCoreForwardsFieldsCallerAndStackToLoza(t *testing.T) {
	sink, store := loza.MemorySink()
	if err := loza.Configure(loza.Test().WithSink(sink)); err != nil {
		t.Fatalf("configure: %v", err)
	}
	t.Cleanup(func() { _ = loza.Shutdown(context.Background()) })

	adapter := Core(zapcore.NewNopCore())
	ent := zapcore.Entry{
		Level:   zapcore.InfoLevel,
		Message: "user action",
		Time:    time.Unix(1, 0),
		Caller:  zapcore.EntryCaller{Defined: true, File: "/srv/app/server.go", Line: 42},
		Stack:   "goroutine 1 [running]:\nmain.main()",
	}
	fields := []zapcore.Field{
		zap.String("user", "bob"),
		zap.Int("attempt", 3),
		zap.Bool("ok", true),
		zap.Duration("elapsed", 2*time.Millisecond),
		zap.NamedError("err", errors.New("boom")),
	}
	if err := adapter.Write(ent, fields); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("loza events = %d, want 1", store.Len())
	}

	ev := store.Events()[0]
	if got, ok := ev.Get("user"); !ok || got != "bob" {
		t.Fatalf("user attr = %v (present=%v), want bob", got, ok)
	}
	if got, ok := ev.Get("attempt"); !ok || got != int64(3) {
		t.Fatalf("attempt attr = %v (%T, present=%v), want int64(3)", got, got, ok)
	}
	if got, ok := ev.Get("ok"); !ok || got != true {
		t.Fatalf("ok attr = %v, want true", got)
	}
	if got, ok := ev.Get("elapsed"); !ok || got != 2*time.Millisecond {
		t.Fatalf("elapsed attr = %v (%T), want 2ms", got, got)
	}
	if got, ok := ev.Get("err"); !ok || got != "boom" {
		t.Fatalf("err attr = %v (present=%v), want boom", got, ok)
	}
	if got, ok := ev.Get("caller"); !ok || got != "app/server.go:42" {
		t.Fatalf("caller attr = %v (present=%v), want app/server.go:42", got, ok)
	}
	if got, ok := ev.Get("stack"); !ok || got != ent.Stack {
		t.Fatalf("stack attr = %v, want %q", got, ent.Stack)
	}
}

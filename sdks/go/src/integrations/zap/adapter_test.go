package zap

import (
	"bytes"
	"context"
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

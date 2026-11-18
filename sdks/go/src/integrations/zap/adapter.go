package zap

import (
	"context"

	"github.com/astraive/loza/sdks/go"
	"go.uber.org/zap/zapcore"
)

// AdapterCore wraps a zapcore.Core and mirrors entries into loza.
type AdapterCore struct {
	zapcore.Core
}

// Core wraps a base zap core with loza mirroring.
func Core(base zapcore.Core) *AdapterCore {
	return &AdapterCore{Core: base}
}

// Deprecated: use Core.
func NewCore(base zapcore.Core) *AdapterCore { return Core(base) }

// With derives a new AdapterCore so loggers created with Logger.With keep
// mirroring into loza. Without this override the embedded core's With would
// return a bare clone and drop the adapter.
func (c *AdapterCore) With(fields []zapcore.Field) zapcore.Core {
	return &AdapterCore{Core: c.Core.With(fields)}
}

// Check adds the AdapterCore (rather than the embedded base core) to the
// CheckedEntry so that CheckedEntry.Write calls AdapterCore.Write. Without
// this override the base core's Check would be promoted and every
// logger.Info/Debug/... call would bypass mirroring entirely.
func (c *AdapterCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

// Write forwards to the wrapped core and emits to loza.
func (c *AdapterCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	ctx := context.Background()
	switch ent.Level {
	case zapcore.DebugLevel:
		loza.DebugContext(ctx, ent.Message, "zap.event")
	case zapcore.InfoLevel:
		loza.InfoContext(ctx, ent.Message, "zap.event")
	case zapcore.WarnLevel:
		loza.WarnContext(ctx, ent.Message, "zap.event")
	default:
		loza.ErrorContext(ctx, ent.Message, nil, "zap.event")
	}
	return c.Core.Write(ent, fields)
}

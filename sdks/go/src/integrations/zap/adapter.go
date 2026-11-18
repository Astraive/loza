package zap

import (
	"context"
	"time"

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
	attrs := fieldsToAttrs(fields)
	if ent.Caller.Defined {
		attrs = append(attrs, loza.String("caller", ent.Caller.TrimmedPath()))
	}
	if ent.Stack != "" {
		attrs = append(attrs, loza.String("stack", ent.Stack))
	}

	ctx := context.Background()
	switch ent.Level {
	case zapcore.DebugLevel:
		loza.DebugContext(ctx, ent.Message, "zap.event", attrs...)
	case zapcore.InfoLevel:
		loza.InfoContext(ctx, ent.Message, "zap.event", attrs...)
	case zapcore.WarnLevel:
		loza.WarnContext(ctx, ent.Message, "zap.event", attrs...)
	default:
		loza.ErrorContext(ctx, ent.Message, nil, "zap.event", attrs...)
	}
	return c.Core.Write(ent, fields)
}

// fieldsToAttrs translates zap structured fields into loza attributes so the
// mirror carries the same context as the wrapped core.
func fieldsToAttrs(fields []zapcore.Field) []loza.Attr {
	if len(fields) == 0 {
		return nil
	}
	attrs := make([]loza.Attr, 0, len(fields))
	for _, f := range fields {
		attrs = append(attrs, fieldToAttrs(f)...)
	}
	return attrs
}

// fieldToAttrs converts a single zapcore.Field into one or more loza.Attrs.
// zap marshals field values through ObjectEncoder.AddTo, so a MapObjectEncoder
// is used to obtain exactly the value the wrapped core would encode, which is
// then mapped to the closest typed loza constructor.
func fieldToAttrs(f zapcore.Field) []loza.Attr {
	switch f.Type {
	case zapcore.UnknownType, zapcore.SkipType, zapcore.NamespaceType:
		// These fields do not carry a standalone key/value pair.
		return nil
	}

	enc := zapcore.NewMapObjectEncoder()
	f.AddTo(enc)

	// Inline marshalers write their fields directly into the encoder rather
	// than under f.Key, so forward every key they produced.
	if f.Type == zapcore.InlineMarshalerType {
		out := make([]loza.Attr, 0, len(enc.Fields))
		for k, v := range enc.Fields {
			out = append(out, valueToAttr(k, v))
		}
		return out
	}

	v, ok := enc.Fields[f.Key]
	if !ok {
		return nil
	}
	return []loza.Attr{valueToAttr(f.Key, v)}
}

// valueToAttr picks a typed loza constructor for a decoded zap field value,
// falling back to a slow-path JSON attr for complex/structured values.
func valueToAttr(key string, v any) loza.Attr {
	switch vv := v.(type) {
	case string:
		return loza.String(key, vv)
	case int:
		return loza.Int(key, vv)
	case int64:
		return loza.Int64(key, vv)
	case uint64:
		return loza.Uint64(key, vv)
	case float64:
		return loza.Float64(key, vv)
	case bool:
		return loza.Bool(key, vv)
	case time.Time:
		return loza.Time(key, vv)
	case time.Duration:
		return loza.Duration(key, vv)
	default:
		return loza.Any(key, v)
	}
}

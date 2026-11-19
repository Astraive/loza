package slog

import (
	"context"
	"log/slog"

	"github.com/astraive/loza/sdks/go"
)

// attrSegment is a batch of attributes captured together with the group stack
// that was active when the corresponding WithAttrs call was made. slog applies
// WithAttrs at the current group depth, so an attribute added before a
// WithGroup must not be nested under that later group.
type attrSegment struct {
	groups []string
	attrs  []slog.Attr
}

// SlogHandler is a slog.Handler that forwards records to loza.
type SlogHandler struct {
	segments []attrSegment
	groups   []string
}

// Handler creates a slog handler backed by the default loza logger.
func Handler() *SlogHandler {
	return &SlogHandler{}
}

// Deprecated: use Handler.
func NewHandler() *SlogHandler { return Handler() }

func (h *SlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return mapLevel(level) >= loza.LevelDebug
}

// groupNode is a node of the temporary tree assembled in Handle. Keeping
// attributes and child groups in insertion order lets the emitted attribute
// list preserve the original WithAttrs/WithGroup call order while merging
// attributes that belong to the same nested group.
type groupNode struct {
	attrs    []loza.Attr
	children []groupChild
	byName   map[string]*groupNode
}

type groupChild struct {
	name string
	node *groupNode
}

func newGroupNode() *groupNode {
	return &groupNode{byName: make(map[string]*groupNode)}
}

func (n *groupNode) child(name string) *groupNode {
	if existing, ok := n.byName[name]; ok {
		return existing
	}
	child := newGroupNode()
	n.byName[name] = child
	n.children = append(n.children, groupChild{name: name, node: child})
	return child
}

func (n *groupNode) nodeAt(groups []string) *groupNode {
	node := n
	for _, name := range groups {
		node = node.child(name)
	}
	return node
}

func (n *groupNode) flatten() []loza.Attr {
	out := make([]loza.Attr, 0, len(n.attrs)+len(n.children))
	out = append(out, n.attrs...)
	for _, c := range n.children {
		out = append(out, loza.Group(c.name, c.node.flatten()...))
	}
	return out
}

func (h *SlogHandler) Handle(ctx context.Context, rec slog.Record) error {
	root := newGroupNode()
	for _, seg := range h.segments {
		node := root.nodeAt(seg.groups)
		for _, a := range seg.attrs {
			node.attrs = append(node.attrs, h.toAttr(a))
		}
	}

	var recordAttrs []slog.Attr
	rec.Attrs(func(a slog.Attr) bool {
		recordAttrs = append(recordAttrs, a)
		return true
	})
	if len(recordAttrs) > 0 {
		// Record attributes are applied at the group depth reached by the
		// chain of WithGroup calls, i.e. the handler's current stack.
		node := root.nodeAt(h.groups)
		for _, a := range recordAttrs {
			node.attrs = append(node.attrs, h.toAttr(a))
		}
	}
	attrs := root.flatten()

	if ev, ok := loza.FromContext(ctx); ok && ev != nil {
		if err := loza.Enrich(ctx, attrs...); err != nil {
			return err
		}
		return nil
	}

	level := mapLevel(rec.Level)
	switch level {
	case loza.LevelDebug:
		loza.DebugContext(ctx, rec.Message, "slog.event", attrs...)
	case loza.LevelInfo:
		loza.InfoContext(ctx, rec.Message, "slog.event", attrs...)
	case loza.LevelWarn:
		loza.WarnContext(ctx, rec.Message, "slog.event", attrs...)
	default:
		loza.ErrorContext(ctx, rec.Message, nil, "slog.event", attrs...)
	}
	return nil
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := *h
	if len(attrs) == 0 {
		return &out
	}
	out.segments = append(append([]attrSegment(nil), h.segments...), attrSegment{
		groups: append([]string(nil), h.groups...),
		attrs:  append([]slog.Attr(nil), attrs...),
	})
	return &out
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	out := *h
	out.groups = append(append([]string(nil), h.groups...), name)
	return &out
}

func (h *SlogHandler) toAttr(a slog.Attr) loza.Attr {
	key := a.Key
	v := a.Value
	switch v.Kind() {
	case slog.KindGroup:
		children := v.Group()
		nested := make([]loza.Attr, 0, len(children))
		for _, child := range children {
			nested = append(nested, h.toAttr(child))
		}
		return loza.Group(key, nested...)
	case slog.KindString:
		return loza.String(key, v.String())
	case slog.KindInt64:
		return loza.Int64(key, v.Int64())
	case slog.KindUint64:
		return loza.Uint64(key, v.Uint64())
	case slog.KindFloat64:
		return loza.Float64(key, v.Float64())
	case slog.KindBool:
		return loza.Bool(key, v.Bool())
	case slog.KindDuration:
		return loza.Duration(key, v.Duration())
	case slog.KindTime:
		return loza.Time(key, v.Time())
	default:
		return loza.Any(key, v.Any())
	}
}

func wrapAttrsWithGroups(attrs []loza.Attr, groups []string) []loza.Attr {
	if len(groups) == 0 || len(attrs) == 0 {
		return attrs
	}
	out := attrs
	for i := len(groups) - 1; i >= 0; i-- {
		out = []loza.Attr{loza.Group(groups[i], out...)}
	}
	return out
}

func mapLevel(level slog.Level) loza.Level {
	switch {
	case level <= slog.LevelDebug:
		return loza.LevelDebug
	case level < slog.LevelWarn:
		return loza.LevelInfo
	case level < slog.LevelError:
		return loza.LevelWarn
	default:
		return loza.LevelError
	}
}

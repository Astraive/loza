package core

import "testing"

func TestNewRespectsDisableExpandDotKeysOnPresetEncoder(t *testing.T) {
	cfg := Production()
	cfg.FieldNaming.ExpandDotKeys = false

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	enc, ok := l.cfg.Encoder.(*JSONEventEncoder)
	if !ok {
		t.Fatalf("expected JSONEventEncoder, got %T", l.cfg.Encoder)
	}
	if enc.ExpandDotKeys {
		t.Fatalf("expected ExpandDotKeys=false when FieldNaming.ExpandDotKeys=false")
	}
}

func TestNewDoesNotMutateSharedPresetEncoder(t *testing.T) {
	// A single caller-owned encoder reused by two configs. One config disables
	// dot-key expansion; that must not change the encoder for the other config.
	shared := &JSONEventEncoder{ExpandDotKeys: true, TimeFormat: TimeFormatRFC3339}

	noExpandCfg := Production()
	noExpandCfg.Encoder = shared
	noExpandCfg.FieldNaming.ExpandDotKeys = false
	lNoExpand, err := New(noExpandCfg)
	if err != nil {
		t.Fatalf("new logger (no expand): %v", err)
	}

	encNoExpand, ok := lNoExpand.cfg.Encoder.(*JSONEventEncoder)
	if !ok {
		t.Fatalf("expected JSONEventEncoder, got %T", lNoExpand.cfg.Encoder)
	}
	if encNoExpand.ExpandDotKeys {
		t.Fatalf("expected logger encoder ExpandDotKeys=false when FieldNaming.ExpandDotKeys=false")
	}
	if encNoExpand == shared {
		t.Fatalf("expected logger to use a copy of the shared encoder, not the shared instance")
	}
	if !shared.ExpandDotKeys {
		t.Fatalf("New mutated the caller-owned shared encoder: ExpandDotKeys should remain true")
	}

	// Reusing the same shared encoder in a config that enables dot keys must
	// still expand dot keys, proving the first New did not change its behavior.
	expandCfg := Production()
	expandCfg.Encoder = shared
	expandCfg.FieldNaming.ExpandDotKeys = true
	lExpand, err := New(expandCfg)
	if err != nil {
		t.Fatalf("new logger (expand): %v", err)
	}
	encExpand, ok := lExpand.cfg.Encoder.(*JSONEventEncoder)
	if !ok {
		t.Fatalf("expected JSONEventEncoder, got %T", lExpand.cfg.Encoder)
	}
	if encExpand != shared {
		t.Fatalf("expected expand config to keep the exact shared encoder instance")
	}
	if !encExpand.ExpandDotKeys {
		t.Fatalf("expected shared encoder to still expand dot keys after reuse")
	}
}

func TestNewDoesNotDuplicateSinkWhenSinkAlsoInSinks(t *testing.T) {
	primary, _ := MemorySink()
	cfg := Test()
	cfg.Sink = primary
	cfg.Sinks = []Sink{primary}

	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	if got := len(l.cfg.Sinks); got != 1 {
		t.Fatalf("expected 1 sink, got %d", got)
	}
}

package core

import "fmt"

func applyDuplicateFieldPolicy(ev *Event, policy DuplicateFieldPolicy) error {
	if ev == nil {
		return nil
	}

	if policy == DropDuplicateAttr {
		policy = CanonicalWins
	}

	ev.MuLock()
	defer ev.MuUnlock()

	// Check under the lock: reading the ev.Attrs slice header without ev.mu
	// races with concurrent setters such as AddAttrs.
	if len(ev.Attrs) == 0 {
		return nil
	}

	out := make([]Attr, 0, len(ev.Attrs))
	for _, a := range ev.Attrs {
		if !isCanonicalKey(a.Key) {
			out = append(out, a)
			continue
		}

		switch policy {
		case CanonicalWins:
			continue
		case AttrWins:
			if ev.applyCanonical(a) {
				continue
			}
			// Keep the canonical key: the encoder already places every extra
			// attr under a single top-level "attrs" object. Prefixing with
			// "attrs." here would make the dot-expanding encoder nest a second
			// "attrs" object (attrs.attrs.<key>).
			out = append(out, a)
		case KeepBothUnderAttrs:
			// Keep the canonical key so the encoder wrapper emits one "attrs"
			// object (attrs.<key>) rather than a doubly nested one.
			out = append(out, a)
		case ErrorOnDuplicate:
			return &DuplicateFieldError{Key: a.Key}
		default:
			return fmt.Errorf("loza: unknown duplicate field policy %d", policy)
		}
	}

	ev.Attrs = out
	return nil
}

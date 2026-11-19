package core

import (
	"strconv"
	"testing"
)

// uuidV7Prefix extracts the (unix_ts_ms, sequence) pair that determines ID
// ordering within a single millisecond from a formatted UUIDv7 string.
func uuidV7Prefix(t *testing.T, id string) (int64, uint16) {
	t.Helper()
	if len(id) != 36 {
		t.Fatalf("malformed UUID %q", id)
	}
	ts, err := strconv.ParseUint(id[0:8]+id[9:13], 16, 48)
	if err != nil {
		t.Fatalf("parse timestamp from %q: %v", id, err)
	}
	v, err := strconv.ParseUint(id[14:18], 16, 16)
	if err != nil {
		t.Fatalf("parse sequence from %q: %v", id, err)
	}
	if version := v >> 12; version != 0x7 {
		t.Fatalf("version nibble = %x, want 7 in %q", version, id)
	}
	return int64(ts), uint16(v & 0x0FFF)
}

// Regression test for issue #686: on sequence overflow the generator used to
// advance the timestamp but left g.lastMS at the old millisecond. The next
// call, still in the new millisecond, then hit the "new millisecond" branch and
// reset seq to 0 again, producing a second ID with the identical (ts, seq=0)
// prefix and leaving ordering to the random tail.
func TestUUIDv7SequenceOverflowContinuesInNewMillisecond(t *testing.T) {
	base := int64(1_700_000_000_000)
	g := &uuidV7Gen{lastMS: base, seq: 0x0FFF} // one increment from overflow

	calls := 0
	g.now = func() int64 {
		calls++
		if calls == 1 {
			return base // entry at base: seq++ overflows
		}
		return base + 1 // wait resolves; subsequent calls stay in new ms
	}

	first := g.newID()  // overflow ID: (base+1, seq 0)
	second := g.newID() // must continue as (base+1, seq 1), not restart at 0

	ts1, seq1 := uuidV7Prefix(t, first)
	ts2, seq2 := uuidV7Prefix(t, second)

	if ts1 != base+1 || ts2 != base+1 {
		t.Fatalf("timestamps = (%d, %d), want both %d", ts1, ts2, base+1)
	}
	if seq1 != 0 {
		t.Fatalf("overflow ID seq = %d, want 0", seq1)
	}
	if seq2 != seq1+1 {
		t.Fatalf("post-overflow seq = %d, want %d (continue sequence, not restart)", seq2, seq1+1)
	}
	if first >= second {
		t.Fatalf("IDs not monotonically increasing after overflow: %q >= %q", first, second)
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// decodeUUIDv7 returns the 48-bit timestamp and 12-bit sequence encoded in a
// UUIDv7 string produced by formatUUID.
func decodeUUIDv7(t *testing.T, id string) (int64, uint16) {
	t.Helper()
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil {
		t.Fatalf("invalid UUID %q: %v", id, err)
	}
	if len(raw) != 16 {
		t.Fatalf("invalid UUID length %d for %q", len(raw), id)
	}
	ms := int64(raw[0])<<40 | int64(raw[1])<<32 | int64(raw[2])<<24 |
		int64(raw[3])<<16 | int64(raw[4])<<8 | int64(raw[5])
	seq := uint16(raw[6]&0x0F)<<8 | uint16(raw[7])
	return ms, seq
}

func TestUUIDv7MonotonicAcrossClockRollback(t *testing.T) {
	// Model a prior ID having used a timestamp ahead of the current wall clock
	// (the state left behind when the system clock jumps backwards).
	rollbackMS := time.Now().UnixMilli() + 5_000
	gen := &uuidV7Gen{lastMS: rollbackMS, seq: 7}

	id := gen.newID()
	ms, seq := decodeUUIDv7(t, id)

	if ms != rollbackMS {
		t.Fatalf("timestamp after rollback = %d, want %d (logical clock held)", ms, rollbackMS)
	}
	if seq != 8 {
		t.Fatalf("sequence after rollback = %d, want 8", seq)
	}
}

func TestUUIDv7MonotonicWithRepeatedRollback(t *testing.T) {
	base := time.Now().UnixMilli() + 5_000
	gen := &uuidV7Gen{lastMS: base}

	var prev string
	for i := 0; i < 10; i++ {
		id := gen.newID()
		if prev != "" && id <= prev {
			t.Fatalf("ID %d not monotonic: %q <= %q", i, id, prev)
		}
		ms, _ := decodeUUIDv7(t, id)
		if ms < base {
			t.Fatalf("ID %d timestamp %d regressed below %d", i, ms, base)
		}
		prev = id
	}
}

func TestUUIDv7SequenceOverflowAdvancesLogicalClock(t *testing.T) {
	base := time.Now().UnixMilli() + 5_000
	gen := &uuidV7Gen{lastMS: base, seq: 0x0FFF}

	id := gen.newID()
	ms, seq := decodeUUIDv7(t, id)
	if ms != base+1 {
		t.Fatalf("timestamp after sequence overflow = %d, want %d", ms, base+1)
	}
	if seq != 0 {
		t.Fatalf("sequence after overflow = %d, want 0", seq)
	}
}

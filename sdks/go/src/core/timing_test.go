package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestProcess_BasicFlow(t *testing.T) {
	ev := newTestEvent(t)
	proc, err := ev.StartProcess("redirect_to_gateway")
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_ = proc.Finish(Int("status_code", 302), String("gateway", "stripe"))

	if len(ev.Processes) != 1 {
		t.Fatalf("expected 1 process, got %d", len(ev.Processes))
	}
	p := ev.Processes[0]
	if p.Step != 1 {
		t.Errorf("expected step 1, got %d", p.Step)
	}
	if p.Name != "redirect_to_gateway" {
		t.Errorf("expected name redirect_to_gateway, got %s", p.Name)
	}
	if p.DurationMS < 1 {
		t.Errorf("expected duration >= 1ms, got %d", p.DurationMS)
	}
	if p.StartedAtMS < 0 {
		t.Errorf("expected started_at_ms >= 0, got %d", p.StartedAtMS)
	}
	if p.EndedAtMS <= p.StartedAtMS {
		t.Errorf("expected ended_at_ms > started_at_ms")
	}
}

func TestProcess_FinishError(t *testing.T) {
	ev := newTestEvent(t)
	proc, _ := ev.StartProcess("payment_attempt")
	time.Sleep(2 * time.Millisecond)
	_ = proc.FinishError(errTest, 504, String("error_code", "gateway_timeout"))

	if len(ev.Processes) != 1 {
		t.Fatalf("expected 1 process, got %d", len(ev.Processes))
	}
	p := ev.Processes[0]
	if p.StatusCode != 504 {
		t.Errorf("expected status_code 504, got %d", p.StatusCode)
	}
}

func TestProcess_StepCounter(t *testing.T) {
	ev := newTestEvent(t)
	for i := 1; i <= 3; i++ {
		proc, _ := ev.StartProcess("step")
		_ = proc.Finish()
	}
	if len(ev.Processes) != 3 {
		t.Fatalf("expected 3 processes, got %d", len(ev.Processes))
	}
	for i, p := range ev.Processes {
		if p.Step != i+1 {
			t.Errorf("process %d: expected step %d, got %d", i, i+1, p.Step)
		}
	}
}

func TestTimer_StartStop(t *testing.T) {
	ev := newTestEvent(t)
	timer, err := ev.StartTimer("stripe.create_session")
	if err != nil {
		t.Fatalf("StartTimer: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_ = timer.Stop(Int("status_code", 200))

	if len(ev.Timers) != 1 {
		t.Fatalf("expected 1 timer, got %d", len(ev.Timers))
	}
	tr := ev.Timers[0]
	if tr.Name != "stripe.create_session" {
		t.Errorf("expected name stripe.create_session, got %s", tr.Name)
	}
	if tr.DurationMS < 1 {
		t.Errorf("expected duration >= 1ms, got %d", tr.DurationMS)
	}
	if tr.StatusCode != 200 {
		t.Errorf("expected status_code 200, got %d", tr.StatusCode)
	}
}

func TestGroup_StartFinish(t *testing.T) {
	ev := newTestEvent(t)
	group, err := ev.StartGroup("payment_flow")
	if err != nil {
		t.Fatalf("StartGroup: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	_ = group.Finish(Int("status_code", 402), String("final_reason", "insufficient_funds"))

	if len(ev.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(ev.Groups))
	}
	g := ev.Groups[0]
	if g.Name != "payment_flow" {
		t.Errorf("expected name payment_flow, got %s", g.Name)
	}
	if g.DurationMS < 1 {
		t.Errorf("expected duration >= 1ms, got %d", g.DurationMS)
	}
	if g.StatusCode != 402 {
		t.Errorf("expected status_code 402, got %d", g.StatusCode)
	}
}

func TestFinishGroupError_RecordsStatusCodeAndError(t *testing.T) {
	ev := newTestEvent(t)
	group, err := ev.StartGroup("shipment")
	if err != nil {
		t.Fatalf("StartGroup: %v", err)
	}
	if err := FinishGroupError(group, errTest, 503); err != nil {
		t.Fatalf("FinishGroupError: %v", err)
	}

	if len(ev.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(ev.Groups))
	}
	g := ev.Groups[0]
	if g.StatusCode != 503 {
		t.Errorf("expected status_code 503, got %d", g.StatusCode)
	}
	var hasErrMsg bool
	for _, a := range g.Attrs {
		if a.Key == "error_message" && a.Value == errTest.Error() {
			hasErrMsg = true
		}
	}
	if !hasErrMsg {
		t.Errorf("expected error_message attr %q, got %+v", errTest.Error(), g.Attrs)
	}
}

func TestPhase_ErrorRecordsStatusCode(t *testing.T) {
	ev := newTestEvent(t)
	ctx := storeEvent(context.Background(), ev)
	wantErr := errors.New("phase failed")
	if err := Phase(ctx, "failing_phase", func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("expected Phase to return the fn error, got %v", err)
	}

	if len(ev.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(ev.Groups))
	}
	g := ev.Groups[0]
	// Error phases must record the same non-zero status as error steps (Step uses 1).
	if g.StatusCode != 1 {
		t.Errorf("expected status_code 1 for failed phase, got %d", g.StatusCode)
	}
}

func TestStopwatch_Elapsed(t *testing.T) {
	sw := Stopwatch()
	time.Sleep(10 * time.Millisecond)
	elapsed := sw.Elapsed()
	if elapsed < 10*time.Millisecond {
		t.Errorf("expected elapsed >= 10ms, got %v", elapsed)
	}
}

func TestTiming_Concurrent(t *testing.T) {
	ev := newTestEvent(t)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			proc, _ := ev.StartProcess("concurrent_step")
			time.Sleep(time.Millisecond)
			_ = proc.Finish()
		}()
	}
	wg.Wait()
	if len(ev.Processes) != 100 {
		t.Errorf("expected 100 processes, got %d", len(ev.Processes))
	}
}

func TestTiming_WireFormat(t *testing.T) {
	ev := newTestEvent(t)
	ev.StartedAt = time.Now()

	proc, _ := ev.StartProcess("redirect_to_gateway")
	time.Sleep(5 * time.Millisecond)
	_ = proc.Finish(Int("status_code", 302), String("gateway", "stripe"))

	group, _ := ev.StartGroup("payment_flow")
	time.Sleep(5 * time.Millisecond)
	_ = group.Finish(Int("status_code", 402))

	timer, _ := ev.StartTimer("stripe.create_session")
	time.Sleep(5 * time.Millisecond)
	_ = timer.Stop(Int("status_code", 200))

	// Verify process
	if len(ev.Processes) != 1 {
		t.Fatalf("expected 1 process, got %d", len(ev.Processes))
	}
	p := ev.Processes[0]
	if p.Step != 1 {
		t.Errorf("expected step 1, got %d", p.Step)
	}
	if p.Name != "redirect_to_gateway" {
		t.Errorf("expected name redirect_to_gateway, got %s", p.Name)
	}
	if p.StatusCode != 302 {
		t.Errorf("expected status_code 302, got %d", p.StatusCode)
	}
	if p.DurationMS < 1 {
		t.Errorf("expected duration_ms >= 1, got %d", p.DurationMS)
	}
	if p.StartedAtMS < 0 {
		t.Errorf("expected started_at_ms >= 0, got %d", p.StartedAtMS)
	}
	if p.EndedAtMS <= p.StartedAtMS {
		t.Errorf("expected ended_at_ms > started_at_ms")
	}

	// Verify groups
	if len(ev.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(ev.Groups))
	}
	g := ev.Groups[0]
	if g.Name != "payment_flow" {
		t.Errorf("expected name payment_flow, got %s", g.Name)
	}
	if g.StatusCode != 402 {
		t.Errorf("expected status_code 402, got %d", g.StatusCode)
	}
	if g.DurationMS < 1 {
		t.Errorf("expected duration_ms >= 1, got %d", g.DurationMS)
	}

	// Verify timers
	if len(ev.Timers) != 1 {
		t.Fatalf("expected 1 timer, got %d", len(ev.Timers))
	}
	tr := ev.Timers[0]
	if tr.Name != "stripe.create_session" {
		t.Errorf("expected name stripe.create_session, got %s", tr.Name)
	}
	if tr.StatusCode != 200 {
		t.Errorf("expected status_code 200, got %d", tr.StatusCode)
	}
	if tr.DurationMS < 1 {
		t.Errorf("expected duration_ms >= 1, got %d", tr.DurationMS)
	}
}

// Regression test for the WithProcess/WithGroup/WithTimer helpers silently
// discarding their attrs argument instead of recording it on the emitted
// process/group/timer.
func TestWithHelpers_ForwardAttrs(t *testing.T) {
	checkAttr := func(t *testing.T, attrs []Attr, key string, want any) {
		t.Helper()
		got, ok := attrValue(attrs, key)
		if !ok {
			t.Errorf("attr %q missing", key)
			return
		}
		if got != want {
			t.Errorf("attr %q = %v, want %v", key, got, want)
		}
	}

	t.Run("process success", func(t *testing.T) {
		ev := newTestEvent(t)
		ctx := storeEvent(context.Background(), ev)
		err := WithProcess(ctx, "validate", func() error { return nil },
			String("gateway", "stripe"), Int("status_code", 202))
		if err != nil {
			t.Fatalf("WithProcess: %v", err)
		}
		if len(ev.Processes) != 1 {
			t.Fatalf("expected 1 process, got %d", len(ev.Processes))
		}
		p := ev.Processes[0]
		checkAttr(t, p.Attrs, "gateway", "stripe")
		if p.StatusCode != 202 {
			t.Errorf("status code = %d, want 202", p.StatusCode)
		}
		if _, ok := attrValue(p.Attrs, "status_code"); ok {
			t.Errorf("status_code should be promoted out of attrs")
		}
	})

	t.Run("group success", func(t *testing.T) {
		ev := newTestEvent(t)
		ctx := storeEvent(context.Background(), ev)
		err := WithGroup(ctx, "checkout", func() error { return nil }, String("result", "ok"))
		if err != nil {
			t.Fatalf("WithGroup: %v", err)
		}
		if len(ev.Groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(ev.Groups))
		}
		checkAttr(t, ev.Groups[0].Attrs, "result", "ok")
	})

	t.Run("timer success", func(t *testing.T) {
		ev := newTestEvent(t)
		ctx := storeEvent(context.Background(), ev)
		err := WithTimer(ctx, "db.query", func() error { return nil },
			String("query", "select 1"), Int("status_code", 200))
		if err != nil {
			t.Fatalf("WithTimer: %v", err)
		}
		if len(ev.Timers) != 1 {
			t.Fatalf("expected 1 timer, got %d", len(ev.Timers))
		}
		tr := ev.Timers[0]
		checkAttr(t, tr.Attrs, "query", "select 1")
		if tr.StatusCode != 200 {
			t.Errorf("status code = %d, want 200", tr.StatusCode)
		}
	})

	t.Run("attrs kept on error paths", func(t *testing.T) {
		wantErr := errors.New("boom")
		ev := newTestEvent(t)
		ctx := storeEvent(context.Background(), ev)

		if err := WithProcess(ctx, "proc", func() error { return wantErr }, String("p", "1")); !errors.Is(err, wantErr) {
			t.Fatalf("WithProcess error = %v, want %v", err, wantErr)
		}
		if len(ev.Processes) != 1 {
			t.Fatalf("expected 1 process, got %d", len(ev.Processes))
		}
		checkAttr(t, ev.Processes[0].Attrs, "p", "1")
		checkAttr(t, ev.Processes[0].Attrs, "error_message", "boom")

		if err := WithGroup(ctx, "grp", func() error { return wantErr }, String("g", "2")); !errors.Is(err, wantErr) {
			t.Fatalf("WithGroup error = %v, want %v", err, wantErr)
		}
		if len(ev.Groups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(ev.Groups))
		}
		checkAttr(t, ev.Groups[0].Attrs, "g", "2")
		checkAttr(t, ev.Groups[0].Attrs, "error_message", "boom")

		if err := WithTimer(ctx, "tmr", func() error { return wantErr }, String("t", "3")); !errors.Is(err, wantErr) {
			t.Fatalf("WithTimer error = %v, want %v", err, wantErr)
		}
		if len(ev.Timers) != 1 {
			t.Fatalf("expected 1 timer, got %d", len(ev.Timers))
		}
		checkAttr(t, ev.Timers[0].Attrs, "t", "3")
		checkAttr(t, ev.Timers[0].Attrs, "error", wantErr)
	})

	t.Run("no event still runs fn", func(t *testing.T) {
		called := false
		if err := WithProcess(context.Background(), "noop", func() error {
			called = true
			return nil
		}, String("ignored", "value")); err != nil {
			t.Fatalf("WithProcess: %v", err)
		}
		if !called {
			t.Error("expected fn to run when ctx has no event")
		}
	})
// Regression tests for MASS-0424: attrs passed to StartProcess/StartTimer/
// StartGroup used to be silently discarded. They must be captured on the
// handle and merged with the attrs supplied at completion.

func TestProcess_StartAttrsRecorded(t *testing.T) {
	ev := newTestEvent(t)
	proc, err := ev.StartProcess("step", String("endpoint", "/v1/charge"))
	if err != nil {
		t.Fatalf("StartProcess: %v", err)
	}
	if err := proc.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got, ok := lookupAttrValue(ev.Processes[0].Attrs, "endpoint"); !ok || got != "/v1/charge" {
		t.Errorf("start-time attr missing: got %v (present=%v)", got, ok)
	}
}

func TestTimer_StartAttrsRecorded(t *testing.T) {
	ev := newTestEvent(t)
	timer, err := ev.StartTimer("db.query", String("pool", "primary"))
	if err != nil {
		t.Fatalf("StartTimer: %v", err)
	}
	if err := timer.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got, ok := lookupAttrValue(ev.Timers[0].Attrs, "pool"); !ok || got != "primary" {
		t.Errorf("start-time attr missing: got %v (present=%v)", got, ok)
	}
}

func TestGroup_StartAttrsRecorded(t *testing.T) {
	ev := newTestEvent(t)
	group, err := ev.StartGroup("checkout", String("tenant", "acme"))
	if err != nil {
		t.Fatalf("StartGroup: %v", err)
	}
	if err := group.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got, ok := lookupAttrValue(ev.Groups[0].Attrs, "tenant"); !ok || got != "acme" {
		t.Errorf("start-time attr missing: got %v (present=%v)", got, ok)
	}
}

func TestTiming_StartAttrsMergedWithFinishAttrs(t *testing.T) {
	ev := newTestEvent(t)

	proc, _ := ev.StartProcess("step", String("origin", "start"), Int("status_code", 200))
	_ = proc.Finish(String("origin", "finish"), String("result", "ok"), Int("status_code", 201))
	p := ev.Processes[0]
	if got, _ := lookupAttrValue(p.Attrs, "origin"); got != "finish" {
		t.Errorf("process: expected finish attr to win, got %v", got)
	}
	if got, ok := lookupAttrValue(p.Attrs, "result"); !ok || got != "ok" {
		t.Errorf("process: finish-only attr missing: got %v (present=%v)", got, ok)
	}
	if p.StatusCode != 201 {
		t.Errorf("process: expected finish status_code 201 to win, got %d", p.StatusCode)
	}

	group, _ := ev.StartGroup("phase", Int("status_code", 200))
	_ = group.Finish()
	g := ev.Groups[0]
	if g.StatusCode != 200 {
		t.Errorf("group: start-time status_code should be honored, got %d", g.StatusCode)
	}

	timer, _ := ev.StartTimer("span", String("source", "start"))
	_ = timer.Stop(String("source", "finish"))
	tr := ev.Timers[0]
	if got, _ := lookupAttrValue(tr.Attrs, "source"); got != "finish" {
		t.Errorf("timer: expected finish attr to win, got %v", got)
	}
}

func TestTiming_WithAliasesRecordAttrs(t *testing.T) {
	ev := newTestEvent(t)
	ctx := storeEvent(context.Background(), ev)
	fn := func() error { return nil }

	if err := WithProcess(ctx, "step", fn, String("kind", "process")); err != nil {
		t.Fatalf("WithProcess: %v", err)
	}
	if got, ok := lookupAttrValue(ev.Processes[0].Attrs, "kind"); !ok || got != "process" {
		t.Errorf("WithProcess: attr missing: got %v (present=%v)", got, ok)
	}

	if err := WithGroup(ctx, "phase", fn, String("kind", "group")); err != nil {
		t.Fatalf("WithGroup: %v", err)
	}
	if got, ok := lookupAttrValue(ev.Groups[0].Attrs, "kind"); !ok || got != "group" {
		t.Errorf("WithGroup: attr missing: got %v (present=%v)", got, ok)
	}

	if err := WithTimer(ctx, "span", fn, String("kind", "timer")); err != nil {
		t.Fatalf("WithTimer: %v", err)
	}
	if got, ok := lookupAttrValue(ev.Timers[0].Attrs, "kind"); !ok || got != "timer" {
		t.Errorf("WithTimer: attr missing: got %v (present=%v)", got, ok)
// TestEventCheckpointUsesConfiguredClock verifies that (*Event).Checkpoint
// measures the delta from StartedAt using the logger's configured clock, the
// same time base used to set StartedAt in buildEvent. With a frozen/mock clock
// whose time is ahead of wall time, using time.Since yields a wrong or negative
// AtMS; this test reproduces that behavior and guards the fix.
func TestEventCheckpointUsesConfiguredClock(t *testing.T) {
	// Put the mock clock well ahead of wall time so the buggy wall-time path
	// produces a large negative AtMS instead of the configured-clock delta.
	start := time.Now().Add(24 * time.Hour)
	fake := NewFakeClock(start)

	cfg := Test()
	cfg.Clock = fake
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx := l.StartEvent(context.Background(), Params{Event: "checkpoint.clock"})
	ev, ok := FromContext(ctx)
	if !ok {
		t.Fatal("expected event in context")
	}
	if !ev.StartedAt.Equal(start) {
		t.Fatalf("StartedAt = %v, want %v", ev.StartedAt, start)
	}

	const advance = 1500 * time.Millisecond
	fake.Advance(advance)

	if err := ev.Checkpoint("step"); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	ev.MuLock()
	defer ev.MuUnlock()
	if len(ev.Checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint, got %d", len(ev.Checkpoints))
	}
	want := advance.Milliseconds()
	if got := ev.Checkpoints[0].AtMS; got != want {
		t.Fatalf("checkpoint AtMS = %d, want %d (configured clock delta)", got, want)
	}
}

var errTest = &testError{msg: "test error"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func newTestEvent(t *testing.T) *Event {
	t.Helper()
	ev := &Event{
		EventID:   "evt_test",
		Timestamp: time.Now(),
		StartedAt: time.Now(),
		Event:     "test.event",
		Kind:      "event",
		Level:     LevelInfo,
		state:     EventStateActive,
	}
	return ev
}

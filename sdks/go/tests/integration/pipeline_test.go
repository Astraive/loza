package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/astraive/loza/sdks/go"
)

func TestAsyncPipelineDrain(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	cfg.Async.Enabled = true
	cfg.Async.QueueSize = 128
	cfg.Async.Workers = 2
	cfg.Async.FlushInterval = 10 * time.Millisecond

	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	for i := 0; i < 50; i++ {
		ctx := loza.StartEvent(context.Background(), loza.Params{Event: "pipeline.test"})
		if err := loza.Finish(ctx, "success"); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if err := loza.Emit(ctx); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}

	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	if got := store.Len(); got < 50 {
		t.Fatalf("expected >=50 events, got %d", got)
	}
	for _, ev := range store.Events() {
		if ev.Event != "pipeline.test" {
			t.Errorf("unexpected event name %q", ev.Event)
		}
		if ev.Outcome != "success" {
			t.Errorf("event %q: unexpected outcome %q", ev.Event, ev.Outcome)
		}
	}
}

func TestAsyncPipelineNoLossConcurrent(t *testing.T) {
	sink, store := loza.MemorySink()
	cfg := loza.Test().WithSink(sink)
	cfg.Async.Enabled = true
	cfg.Async.QueueSize = 256
	cfg.Async.Workers = 4
	cfg.Async.FlushInterval = 10 * time.Millisecond
	cfg.Async.Backpressure = loza.Block

	if err := loza.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}

	const goroutines = 16
	const perGoroutine = 300
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ctx := loza.StartEvent(context.Background(), loza.Params{Event: "pipeline.concurrent"})
				if err := loza.Enrich(ctx, loza.Int("worker", gid), loza.Int("seq", i)); err != nil {
					t.Errorf("enrich: %v", err)
					return
				}
				if err := loza.Finish(ctx, "success"); err != nil {
					t.Errorf("finish: %v", err)
					return
				}
				if err := loza.Emit(ctx); err != nil {
					t.Errorf("emit: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if err := loza.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	want := goroutines * perGoroutine
	if got := store.Len(); got != want {
		t.Fatalf("expected %d events, got %d", want, got)
	}
	if err := verifyConcurrentEvents(store.Events(), goroutines, perGoroutine); err != nil {
		t.Fatalf("event contents: %v", err)
	}
}

// verifyConcurrentEvents checks that the sink holds exactly one well-formed
// event for every (worker, seq) pair the pipeline produced. A count check alone
// is insufficient: a failed Enrich/Finish still leaves an event in the store,
// so the event name, outcome and attributes must be verified too.
func verifyConcurrentEvents(events []*loza.Event, goroutines, perGoroutine int) error {
	seen := make(map[[2]int]int, goroutines*perGoroutine)
	for _, ev := range events {
		if ev == nil {
			return fmt.Errorf("nil event in store")
		}
		if ev.Event != "pipeline.concurrent" {
			return fmt.Errorf("unexpected event name %q", ev.Event)
		}
		if ev.Outcome != "success" {
			return fmt.Errorf("event %q: unexpected outcome %q", ev.Event, ev.Outcome)
		}
		var worker, seq int
		var haveWorker, haveSeq bool
		for _, a := range ev.AttrList() {
			switch a.Key {
			case "worker":
				v, ok := a.Value.(int)
				if !ok {
					return fmt.Errorf("event worker attr has type %T, want int", a.Value)
				}
				worker, haveWorker = v, true
			case "seq":
				v, ok := a.Value.(int)
				if !ok {
					return fmt.Errorf("event seq attr has type %T, want int", a.Value)
				}
				seq, haveSeq = v, true
			}
		}
		if !haveWorker || !haveSeq {
			return fmt.Errorf("event missing worker/seq attrs (haveWorker=%t haveSeq=%t)", haveWorker, haveSeq)
		}
		seen[[2]int{worker, seq}]++
	}

	want := goroutines * perGoroutine
	if len(seen) != want {
		return fmt.Errorf("expected %d distinct (worker,seq) pairs, got %d", want, len(seen))
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			key := [2]int{g, i}
			if seen[key] != 1 {
				return fmt.Errorf("(worker,seq)=%v appeared %d times, want exactly 1", key, seen[key])
			}
		}
	}
	return nil
}

// TestVerifyConcurrentEventsDetectsMissingOrMalformedData is a regression test
// for the count-only assertion: a store can hold the right number of events
// while the intended data is missing. The verifier must reject such events.
func TestVerifyConcurrentEventsDetectsMissingOrMalformedData(t *testing.T) {
	makeEvents := func() []*loza.Event {
		var events []*loza.Event
		for g := 0; g < 2; g++ {
			for i := 0; i < 3; i++ {
				events = append(events, &loza.Event{
					Event:   "pipeline.concurrent",
					Outcome: "success",
					Attrs:   []loza.Attr{loza.Int("worker", g), loza.Int("seq", i)},
				})
			}
		}
		return events
	}

	if err := verifyConcurrentEvents(makeEvents(), 2, 3); err != nil {
		t.Fatalf("well-formed events should verify: %v", err)
	}

	t.Run("count matches but attrs are missing", func(t *testing.T) {
		events := makeEvents()
		events[0].Attrs = nil
		if err := verifyConcurrentEvents(events, 2, 3); err == nil {
			t.Fatal("expected verifier to reject event with missing worker/seq attrs")
		}
	})

	t.Run("wrong outcome", func(t *testing.T) {
		events := makeEvents()
		events[1].Outcome = "failure"
		if err := verifyConcurrentEvents(events, 2, 3); err == nil {
			t.Fatal("expected verifier to reject event with wrong outcome")
		}
	})

	t.Run("duplicate pair", func(t *testing.T) {
		events := makeEvents()
		events[2].Attrs = []loza.Attr{loza.Int("worker", 0), loza.Int("seq", 0)}
		if err := verifyConcurrentEvents(events, 2, 3); err == nil {
			t.Fatal("expected verifier to reject duplicate (worker,seq) pair")
		}
	})
}

func TestConfigureDrainsPreviousLogger(t *testing.T) {
	sinkA, storeA := loza.MemorySink()
	cfgA := loza.Test().WithSink(sinkA)
	cfgA.Async.Enabled = true
	cfgA.Async.QueueSize = 256
	cfgA.Async.Workers = 2
	cfgA.Async.FlushInterval = time.Second
	cfgA.Async.Backpressure = loza.Block
	if err := loza.Configure(cfgA); err != nil {
		t.Fatalf("configure A: %v", err)
	}

	const total = 400
	for i := 0; i < total; i++ {
		ctx := loza.StartEvent(context.Background(), loza.Params{Event: "pipeline.reconfigure"})
		if err := loza.Finish(ctx, "success"); err != nil {
			t.Fatalf("finish A: %v", err)
		}
		if err := loza.Emit(ctx); err != nil {
			t.Fatalf("emit A: %v", err)
		}
	}

	sinkB, _ := loza.MemorySink()
	cfgB := loza.Test().WithSink(sinkB)
	cfgB.Async.Enabled = true
	cfgB.Async.QueueSize = 128
	cfgB.Async.Workers = 2
	cfgB.Async.FlushInterval = 10 * time.Millisecond
	cfgB.Async.Backpressure = loza.Block
	if err := loza.Configure(cfgB); err != nil {
		t.Fatalf("configure B: %v", err)
	}

	if got := storeA.Len(); got != total {
		t.Fatalf("expected %d events from previous logger after reconfigure, got %d", total, got)
	}
	for _, ev := range storeA.Events() {
		if ev.Event != "pipeline.reconfigure" {
			t.Errorf("unexpected event name %q", ev.Event)
		}
		if ev.Outcome != "success" {
			t.Errorf("event %q: unexpected outcome %q", ev.Event, ev.Outcome)
		}
	}
}

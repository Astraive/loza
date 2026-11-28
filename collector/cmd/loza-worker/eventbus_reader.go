package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/astraive/loza/collector/internal/eventbus"
)

// eventbusQueueReader implements queueReader using the eventbus abstraction.
type eventbusQueueReader struct {
	bus    eventbus.Bus
	topic  string
	group  string
	ch     chan queueRecord
	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
}

func newEventbusQueueReader(bus eventbus.Bus, topic, group string) *eventbusQueueReader {
	return &eventbusQueueReader{
		bus:   bus,
		topic: topic,
		group: group,
		ch:    make(chan queueRecord, 1000),
	}
}

func (r *eventbusQueueReader) Poll(ctx context.Context, timeout time.Duration) ([]queueRecord, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("reader closed")
	}
	r.mu.Unlock()

	// Subscribe on first poll
	if r.cancel == nil {
		subCtx, cancel := context.WithCancel(ctx)
		r.cancel = cancel

		handler := func(hCtx context.Context, msg eventbus.Message) error {
			env := msg.Envelope()
			committed := make(chan error, 1)
			rec := queueRecord{
				value: append([]byte(nil), env.Body...),
				commit: func(cCtx context.Context) error {
					err := msg.Ack(cCtx)
					committed <- err
					return err
				},
			}
			// Some adapters acknowledge when the handler returns nil, so keep
			// it open until the worker invokes the commit callback.
			select {
			case r.ch <- rec:
				select {
				case err := <-committed:
					return err
				case <-hCtx.Done():
					return hCtx.Err()
				case <-subCtx.Done():
					return subCtx.Err()
				}
			case <-hCtx.Done():
				return hCtx.Err()
			case <-subCtx.Done():
				return subCtx.Err()
			}
		}

		if err := r.bus.Subscribe(subCtx, r.topic, r.group, handler); err != nil {
			cancel()
			return nil, fmt.Errorf("eventbus subscribe: %w", err)
		}
	}

	// Collect records with timeout
	var records []queueRecord
	deadline := time.After(timeout)

	select {
	case rec := <-r.ch:
		records = append(records, rec)
		// Drain any additional records that are immediately available
		for {
			select {
			case rec := <-r.ch:
				records = append(records, rec)
			default:
				return records, nil
			}
		}
	case <-deadline:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *eventbusQueueReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.cancel != nil {
		r.cancel()
	}
	return r.bus.Close(context.Background())
}

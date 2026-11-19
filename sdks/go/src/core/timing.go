package core

import (
	"context"
	"time"
)

// EventProcess represents a named step in a multi-step process.
// Processes are recorded in the "processes" array in the emitted JSON.
type EventProcess struct {
	Step        int
	Name        string
	StatusCode  int
	StartedAtMS int64
	EndedAtMS   int64
	DurationMS  int64
	Attrs       []Attr
}

// EventGroup represents a parent phase containing processes.
// Groups are recorded in the "groups" array in the emitted JSON.
type EventGroup struct {
	Name        string
	StatusCode  int
	StartedAtMS int64
	EndedAtMS   int64
	DurationMS  int64
	Attrs       []Attr
}

// EventTimer represents a named duration measurement.
// Timers are recorded in the "timers" array in the emitted JSON.
type EventTimer struct {
	Name       string
	DurationMS int64
	StatusCode int
	Attrs      []Attr
}

// ProcessHandle is returned by Event.StartProcess and tracks a running process step.
type ProcessHandle struct {
	event     *Event
	name      string
	step      int
	startedAt time.Time
}

// Finish completes the process with the given attrs.
func (h *ProcessHandle) Finish(attrs ...Attr) error {
	return h.finishInternal(0, attrs)
}

// FinishError completes the process with an error status code and error info.
func (h *ProcessHandle) FinishError(err error, statusCode int, attrs ...Attr) error {
	if err != nil {
		attrs = append(attrs, Attr{Key: "error_message", Kind: KindString, Value: err.Error()})
	}
	return h.finishInternal(statusCode, attrs)
}

func (h *ProcessHandle) finishInternal(statusCode int, attrs []Attr) error {
	now := time.Now()
	startedMS := h.startedAt.Sub(h.event.StartedAt).Milliseconds()
	endedMS := now.Sub(h.event.StartedAt).Milliseconds()

	// If statusCode is 0, try to extract from attrs
	if statusCode == 0 {
		statusCode = extractStatusCode(attrs)
		attrs = removeStatusCode(attrs)
	}

	entry := EventProcess{
		Step:        h.step,
		Name:        h.name,
		StatusCode:  statusCode,
		StartedAtMS: startedMS,
		EndedAtMS:   endedMS,
		DurationMS:  endedMS - startedMS,
		Attrs:       attrs,
	}

	h.event.mu.Lock()
	h.event.Processes = append(h.event.Processes, entry)
	h.event.mu.Unlock()
	return nil
}

// Duration returns the elapsed duration since the process started.
func (h *ProcessHandle) Duration() time.Duration {
	return time.Since(h.startedAt)
}

// TimerHandle is returned by Event.StartTimer and tracks a running timer.
type TimerHandle struct {
	event     *Event
	name      string
	startedAt time.Time
}

// Stop completes the timer with the given attrs.
func (h *TimerHandle) Stop(attrs ...Attr) error {
	now := time.Now()
	durationMS := now.Sub(h.startedAt).Milliseconds()

	statusCode := extractStatusCode(attrs)
	attrs = removeStatusCode(attrs)

	entry := EventTimer{
		Name:       h.name,
		DurationMS: durationMS,
		StatusCode: statusCode,
		Attrs:      attrs,
	}

	h.event.mu.Lock()
	h.event.Timers = append(h.event.Timers, entry)
	h.event.mu.Unlock()
	return nil
}

// Duration returns the elapsed duration since the timer started.
func (h *TimerHandle) Duration() time.Duration {
	return time.Since(h.startedAt)
}

// GroupHandle is returned by Event.StartGroup and tracks a running group phase.
type GroupHandle struct {
	event     *Event
	name      string
	startedAt time.Time
}

// Finish completes the group with the given attrs.
func (h *GroupHandle) Finish(attrs ...Attr) error {
	return h.finishInternal(0, attrs)
}

// FinishError completes the group with an error status code.
func (h *GroupHandle) FinishError(statusCode int, attrs ...Attr) error {
	return h.finishInternal(statusCode, attrs)
}

func (h *GroupHandle) finishInternal(statusCode int, attrs []Attr) error {
	now := time.Now()
	startedMS := h.startedAt.Sub(h.event.StartedAt).Milliseconds()
	endedMS := now.Sub(h.event.StartedAt).Milliseconds()

	// If statusCode is 0, try to extract from attrs
	if statusCode == 0 {
		statusCode = extractStatusCode(attrs)
		attrs = removeStatusCode(attrs)
	}

	entry := EventGroup{
		Name:        h.name,
		StatusCode:  statusCode,
		StartedAtMS: startedMS,
		EndedAtMS:   endedMS,
		DurationMS:  endedMS - startedMS,
		Attrs:       attrs,
	}

	h.event.mu.Lock()
	h.event.Groups = append(h.event.Groups, entry)
	h.event.mu.Unlock()
	return nil
}

// Duration returns the elapsed duration since the group started.
func (h *GroupHandle) Duration() time.Duration {
	return time.Since(h.startedAt)
}

// StopwatchHandle is a standalone timer that measures elapsed time without an event reference.
type StopwatchHandle struct {
	startedAt time.Time
}

// Stopwatch creates a new standalone stopwatch.
func Stopwatch() *StopwatchHandle {
	return &StopwatchHandle{startedAt: time.Now()}
}

// Elapsed returns the duration since the stopwatch was created.
func (h *StopwatchHandle) Elapsed() time.Duration {
	return time.Since(h.startedAt)
}

// FinishGroupError completes the group with an error status code and error info.
func FinishGroupError(handle *GroupHandle, err error, statusCode int, attrs ...Attr) error {
	if err != nil {
		attrs = append(attrs, Attr{Key: "error_message", Kind: KindString, Value: err.Error()})
	}
	return handle.FinishError(statusCode, attrs...)
}

// Measure runs fn, measures its duration, and returns it as an Attr.
func Measure(name string, fn func()) Attr {
	start := time.Now()
	fn()
	return Duration(name, time.Since(start))
}

// Step runs fn as a named process step on the event in ctx.
func Step(ctx context.Context, name string, fn func() error) error {
	return step(ctx, name, fn, nil)
}

// step is the shared implementation of Step and WithProcess. attrs are recorded
// on the emitted process on both the success and error paths.
func step(ctx context.Context, name string, fn func() error, attrs []Attr) error {
	ev := loadEvent(ctx)
	if ev == nil {
		return fn()
	}
	h, err := ev.StartProcess(name)
	if err != nil {
		return err
	}
	// Copy so appending the error message cannot mutate a caller-owned slice.
	attrs = append([]Attr(nil), attrs...)
	if err := fn(); err != nil {
		_ = h.FinishError(err, 1, attrs...)
		return err
	}
	return h.Finish(attrs...)
}

// Phase runs fn as a named group phase on the event in ctx.
func Phase(ctx context.Context, name string, fn func() error) error {
	return phase(ctx, name, fn, nil)
}

// phase is the shared implementation of Phase and WithGroup.
func phase(ctx context.Context, name string, fn func() error, attrs []Attr) error {
	ev := loadEvent(ctx)
	if ev == nil {
		return fn()
	}
	h, err := ev.StartGroup(name)
	if err != nil {
		return err
	}
	attrs = append([]Attr(nil), attrs...)
	if err := fn(); err != nil {
		// Preserve both main's status code (1) and the PR's attr forwarding.
		_ = FinishGroupError(h, err, 1, attrs...)
		return err
	}
	return h.Finish(attrs...)
}

// Span runs fn as a named timer span on the event in ctx.
func Span(ctx context.Context, name string, fn func() error) error {
	return span(ctx, name, fn, nil)
}

// span is the shared implementation of Span and WithTimer.
func span(ctx context.Context, name string, fn func() error, attrs []Attr) error {
	ev := loadEvent(ctx)
	if ev == nil {
		return fn()
	}
	h, err := ev.StartTimer(name)
	if err != nil {
		return err
	}
	if err := fn(); err != nil {
		errAttrs := make([]Attr, 0, len(attrs)+1)
		errAttrs = append(errAttrs, attrs...)
		errAttrs = append(errAttrs, Err(err))
		_ = h.Stop(errAttrs...)
		return err
	}
	return h.Stop(attrs...)
}

// WithProcess is like Step but also records attrs on the emitted process.
func WithProcess(ctx context.Context, name string, fn func() error, attrs ...Attr) error {
	return step(ctx, name, fn, attrs)
}

// WithGroup is like Phase but also records attrs on the emitted group.
func WithGroup(ctx context.Context, name string, fn func() error, attrs ...Attr) error {
	return phase(ctx, name, fn, attrs)
}

// WithTimer is like Span but also records attrs on the emitted timer.
func WithTimer(ctx context.Context, name string, fn func() error, attrs ...Attr) error {
	return span(ctx, name, fn, attrs)
}

// extractStatusCode finds and returns the status_code value from attrs, or 0 if not found.
func extractStatusCode(attrs []Attr) int {
	for _, a := range attrs {
		if a.Key == "status_code" {
			switch v := a.Value.(type) {
			case int:
				return v
			case int64:
				return int(v)
			case float64:
				return int(v)
			}
		}
	}
	return 0
}

// removeStatusCode returns a new attrs slice without the status_code entry.
func removeStatusCode(attrs []Attr) []Attr {
	result := make([]Attr, 0, len(attrs))
	for _, a := range attrs {
		if a.Key != "status_code" {
			result = append(result, a)
		}
	}
	return result
}

package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

type captureRoundTripper struct {
	req  *http.Request
	resp *http.Response
	err  error
}

func (rt *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.req = req
	if rt.resp != nil || rt.err != nil {
		return rt.resp, rt.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestRoundTripperInjectsTraceContext(t *testing.T) {
	ctx := traceContextWithState(t, "")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	base := &captureRoundTripper{}
	rt := NewRoundTripper(base)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if base.req == nil {
		t.Fatalf("expected request to be forwarded")
	}
	if base.req.Header.Get("traceparent") == "" {
		t.Fatalf("expected traceparent header injection")
	}
}

func TestRoundTripperInjectsTraceState(t *testing.T) {
	ctx := traceContextWithState(t, "vendor=value")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	base := &captureRoundTripper{}
	rt := NewRoundTripper(base)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if base.req == nil {
		t.Fatalf("expected request to be forwarded")
	}
	if base.req.Header.Get("tracestate") != "vendor=value" {
		t.Fatalf("expected tracestate header injection")
	}
}

func TestRoundTripperCheckpointsIncludeHTTPAttrs(t *testing.T) {
	cfg := Test()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	prev := Default()
	SetDefault(l)
	defer SetDefault(prev)

	ctx := Default().StartEvent(context.Background(), Params{Event: "http.client.test"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	base := &captureRoundTripper{
		resp: &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
			Request:    req,
		},
	}
	if _, err := NewRoundTripper(base).RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}

	started := mustCheckpointByName(t, ctx, "http.client.started")
	if got, ok := attrValue(started.Attrs, "http.client.method"); !ok || got != http.MethodPost {
		t.Fatalf("expected checkpoint method attr")
	}
	if got, ok := attrValue(started.Attrs, "http.client.url"); !ok || got != "https://example.com/api" {
		t.Fatalf("expected checkpoint url attr")
	}

	finished := mustCheckpointByName(t, ctx, "http.client.finished")
	if got, ok := attrValue(finished.Attrs, "http.client.status_code"); !ok || got != http.StatusCreated {
		t.Fatalf("expected checkpoint status_code attr")
	}
	if got, ok := attrValue(finished.Attrs, "http.client.duration_ms"); !ok {
		t.Fatalf("expected checkpoint duration attr")
	} else if durationMS, ok := got.(int64); !ok || durationMS < 0 {
		t.Fatalf("expected non-negative int64 duration attr")
	}
}

func TestRoundTripperCheckpointURLStripsSensitiveComponents(t *testing.T) {
	cfg := Test()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	prev := Default()
	SetDefault(l)
	defer SetDefault(prev)

	ctx := Default().StartEvent(context.Background(), Params{Event: "http.client.test"})
	// Userinfo and query parameters both carry secrets that must not be
	// captured by the "http.client.url" telemetry attribute.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://user:secret@example.com/api/v1?api_key=SUPERSECRET123&token=abc", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	if _, err := NewRoundTripper(&captureRoundTripper{}).RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}

	started := mustCheckpointByName(t, ctx, "http.client.started")
	got, ok := attrValue(started.Attrs, "http.client.url")
	if !ok {
		t.Fatalf("expected checkpoint url attr")
	}
	gotURL, ok := got.(string)
	if !ok {
		t.Fatalf("expected string url attr, got %T", got)
	}
	if want := "https://example.com/api/v1"; gotURL != want {
		t.Fatalf("telemetry url = %q, want %q", gotURL, want)
	}
	for _, secret := range []string{"user", "secret", "api_key", "SUPERSECRET123", "token", "abc"} {
		if strings.Contains(gotURL, secret) {
			t.Fatalf("telemetry url %q leaked sensitive component %q", gotURL, secret)
		}
	}
}

func TestRoundTripperCheckpointsIncludeErrorAttr(t *testing.T) {
	cfg := Test()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	prev := Default()
	SetDefault(l)
	defer SetDefault(prev)

	ctx := Default().StartEvent(context.Background(), Params{Event: "http.client.test"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/fail", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	callErr := errors.New("dial failed")
	base := &captureRoundTripper{err: callErr}
	if _, err := NewRoundTripper(base).RoundTrip(req); !errors.Is(err, callErr) {
		t.Fatalf("expected roundtrip error")
	}

	finished := mustCheckpointByName(t, ctx, "http.client.finished")
	if got, ok := attrValue(finished.Attrs, "http.client.error"); !ok || got != callErr.Error() {
		t.Fatalf("expected checkpoint error attr")
	}
	if got, ok := attrValue(finished.Attrs, "http.client.duration_ms"); !ok {
		t.Fatalf("expected checkpoint duration attr")
	} else if durationMS, ok := got.(int64); !ok || durationMS < 0 {
		t.Fatalf("expected non-negative int64 duration attr")
	}
}

func TestRoundTripperErrorAttrRedactsURLCredentials(t *testing.T) {
	cfg := Test()
	l, err := New(cfg)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	prev := Default()
	SetDefault(l)
	defer SetDefault(prev)

	ctx := Default().StartEvent(context.Background(), Params{Event: "http.client.test"})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://apiuser:pw_supersecret@example.com/api?token=tok_9f8e7d&api_key=key_123456", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	// A *url.Error embeds the full request URL, including its credentials and
	// query tokens. Reproduces the leak described in the finding.
	callErr := &url.Error{
		Op:  "Get",
		URL: req.URL.String(),
		Err: errors.New("dial tcp: connection refused"),
	}
	base := &captureRoundTripper{err: callErr}
	if _, err := NewRoundTripper(base).RoundTrip(req); !errors.Is(err, callErr) {
		t.Fatalf("expected roundtrip error")
	}

	finished := mustCheckpointByName(t, ctx, "http.client.finished")
	got, ok := attrValue(finished.Attrs, "http.client.error")
	if !ok {
		t.Fatalf("expected checkpoint error attr")
	}
	msg, ok := got.(string)
	if !ok {
		t.Fatalf("expected string error attr, got %T", got)
	}
	for _, secret := range []string{"pw_supersecret", "tok_9f8e7d", "key_123456", "apiuser:pw_supersecret@"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("error attr leaked secret %q: %s", secret, msg)
		}
	}
	if !strings.Contains(msg, "example.com/api") {
		t.Fatalf("error attr lost useful host/path context: %s", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Fatalf("error attr lost underlying error context: %s", msg)
	}
}

func TestSanitizeClientErrorRedactsBareEmbeddedURL(t *testing.T) {
	// Transport errors may embed a URL without wrapping it in *url.Error.
	err := errors.New(`request to "https://u:pw_hunter2@host.test/v1?sig=sig_deadbeef" failed`)
	msg := sanitizeClientError(err)
	for _, secret := range []string{"pw_hunter2", "sig_deadbeef", "u:pw_hunter2@"} {
		if strings.Contains(msg, secret) {
			t.Fatalf("sanitized error leaked secret %q: %s", secret, msg)
		}
	}
	if !strings.Contains(msg, "host.test/v1") || !strings.Contains(msg, "failed") {
		t.Fatalf("sanitized error lost useful context: %s", msg)
	}
}

func traceContextWithState(t *testing.T, tracestate string) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}

	cfg := trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	}
	if tracestate != "" {
		state, err := trace.ParseTraceState(tracestate)
		if err != nil {
			t.Fatalf("parse tracestate: %v", err)
		}
		cfg.TraceState = state
	}
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(cfg))
}

func mustCheckpointByName(t *testing.T, ctx context.Context, name string) EventCheckpoint {
	t.Helper()

	ev, ok := FromContext(ctx)
	if !ok {
		t.Fatalf("expected event in context")
	}
	ev.MuLock()
	defer ev.MuUnlock()

	for _, cp := range ev.Checkpoints {
		if cp.Name == name {
			return cp
		}
	}
	t.Fatalf("expected checkpoint %q", name)
	return EventCheckpoint{}
}

func attrValue(attrs []Attr, key string) (any, bool) {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value, true
		}
	}
	return nil, false
}

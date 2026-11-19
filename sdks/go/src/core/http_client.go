package core

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// telemetryRequestURL returns a telemetry-safe representation of u that keeps
// only the scheme, host and path. Userinfo (user:pass@host), query parameters,
// fragments and opaque data are deliberately excluded because they commonly
// carry credentials, tokens or other sensitive information that must not be
// persisted or exported to the telemetry backend.
func telemetryRequestURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	safe := *u
	safe.User = nil
	safe.RawQuery = ""
	safe.ForceQuery = false
	safe.Fragment = ""
	safe.RawFragment = ""
	safe.Opaque = ""
	return safe.String()
}

type lozaRoundTripper struct {
	base http.RoundTripper
}

// NewRoundTripper wraps a base transport and enriches active events with outbound HTTP metadata.
func NewRoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &lozaRoundTripper{base: base}
}

// WrapHTTPClient wraps an existing client with LOZA outbound instrumentation.
func WrapHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	cp := *client
	cp.Transport = NewRoundTripper(client.Transport)
	return &cp
}

func (rt *lozaRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	ctx := req.Context()
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		req = req.Clone(ctx)
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(req.Header))
	}
	if HasEvent(ctx) {
		_ = Default().Checkpoint(ctx, "http.client.started",
			String("http.client.method", req.Method),
			String("http.client.url", telemetryRequestURL(req.URL)),
		)
	}
	resp, err := rt.base.RoundTrip(req)
	if HasEvent(ctx) {
		attrs := []Attr{
			Int64("http.client.duration_ms", time.Since(start).Milliseconds()),
		}
		if resp != nil {
			attrs = append(attrs, Int("http.client.status_code", resp.StatusCode))
		}
		if err != nil {
			attrs = append(attrs, String("http.client.error", sanitizeClientError(err)))
		}
		_ = Default().Checkpoint(ctx, "http.client.finished", attrs...)
	}
	return resp, err
}

// clientErrorURLPattern matches absolute URLs embedded in free-form error text.
var clientErrorURLPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>]+`)

// sanitizeClientError removes sensitive URL components from transport error
// text before it is recorded as a telemetry attribute. Go's *url.Error embeds
// the full request URL, which can carry userinfo credentials and query tokens;
// those must not be exported. Benign error text is returned unchanged.
func sanitizeClientError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.URL != "" {
		msg = strings.ReplaceAll(msg, urlErr.URL, sanitizeTelemetryURL(urlErr.URL))
	}
	return clientErrorURLPattern.ReplaceAllStringFunc(msg, sanitizeTelemetryURL)
}

// sanitizeTelemetryURL drops userinfo, query and fragment from a URL while
// preserving scheme, host and path so the error remains useful for debugging.
func sanitizeTelemetryURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User == nil && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == "" && parsed.RawFragment == "" {
		return raw
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

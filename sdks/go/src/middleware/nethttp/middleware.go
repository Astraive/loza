package nethttp

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/astraive/loza/sdks/go"
)

// Config configures the net/http middleware.
type Config struct {
	Event string
	// RouteExtractor may provide a route template (e.g. "/users/{id}") for a request.
	RouteExtractor func(*http.Request) string
	// TrustForwardedFor controls whether X-Forwarded-For (or ForwardedForHeader) is trusted.
	// Default is false to avoid spoofed client IP logging.
	TrustForwardedFor bool
	// ForwardedForHeader optionally overrides the forwarded-for header name.
	// Default: X-Forwarded-For.
	ForwardedForHeader string
	// HeaderAttrs controls which inbound request headers are copied into attrs as
	// "http.header.<normalized-name>".
	HeaderAttrs []string
}

// Middleware starts a canonical event at request start and emits it on completion.
func Middleware(cfg ...Config) func(http.Handler) http.Handler {
	c := Config{}
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return MiddlewareWithConfig(c)
}

// MiddlewareWithConfig starts a canonical event at request start and emits it on completion.
func MiddlewareWithConfig(cfg Config) func(http.Handler) http.Handler {
	eventName := cfg.Event
	if eventName == "" {
		eventName = "http.request"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Register recovery and cleanup before any request setup. Setup includes
			// the user-supplied RouteExtractor, attribute construction and response
			// writer wrapping, any of which may panic. Tracking how far setup got
			// lets the defer always emit an event that was actually started, and
			// still write a 500 when no wrapped writer exists yet.
			rw := w
			var state *responseWriter
			var ctx context.Context
			eventStarted := false

			defer func() {
				rec := recover()

				if rec != nil && (state == nil || !state.wroteHeader) {
					http.Error(rw, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}

				// Read counters after writing the 500 so the event reports the
				// status code and body that were actually sent to the client.
				statusCode := http.StatusOK
				var responseBytes int64
				if state != nil {
					statusCode = state.statusCode
					responseBytes = int64(state.bytes)
				}

				if rec != nil {
					if eventStarted {
						loza.FinishError(ctx, panicErr{value: rec},
							loza.Int("status_code", statusCode),
							loza.Int64("response_bytes", responseBytes),
						)
						_ = loza.Emit(ctx)
					}
					return
				}

				if !eventStarted {
					return
				}
				loza.Finish(ctx, outcomeFromStatus(statusCode),
					loza.Int("status_code", statusCode),
					loza.Int64("response_bytes", responseBytes),
				)
				_ = loza.Emit(ctx)
			}()

			route := ""
			if cfg.RouteExtractor != nil {
				route = cfg.RouteExtractor(r)
			}

			requestID := r.Header.Get("X-Request-ID")
			traceID := r.Header.Get("X-Trace-ID")
			requestBytes := r.ContentLength
			if requestBytes < 0 {
				requestBytes = 0
			}

			customAttrs := []loza.Attr{
				loza.String("scheme", schemeFromRequest(r)),
				loza.String("user_agent", r.UserAgent()),
				loza.String("remote_ip", clientIP(r, cfg)),
				loza.Int64("request_bytes", requestBytes),
			}
			customAttrs = append(customAttrs, selectedHeaderAttrs(r, cfg.HeaderAttrs)...)

			ctx = loza.StartHTTPEvent(r.Context(), loza.Params{
				Event:     eventName,
				Kind:      "http",
				Method:    r.Method,
				Path:      r.URL.Path,
				Route:     route,
				Host:      r.Host,
				RequestID: requestID,
				TraceID:   traceID,
				Custom:    customAttrs,
			})
			eventStarted = true

			rw, state = newResponseWriter(w)

			next.ServeHTTP(rw, r.WithContext(ctx))
		})
	}
}

// outcomeFromStatus maps an HTTP response status to a canonical event outcome.
// Client (4xx) and server (5xx) error responses must not be reported as "success".
func outcomeFromStatus(statusCode int) string {
	if statusCode >= http.StatusBadRequest {
		return "error"
	}
	return "success"
}

type panicErr struct {
	value any
}

func (e panicErr) Error() string {
	return fmt.Sprintf("panic recovered: %v", e.value)
}

func schemeFromRequest(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func clientIP(r *http.Request, cfg Config) string {
	if cfg.TrustForwardedFor {
		header := cfg.ForwardedForHeader
		if header == "" {
			header = "X-Forwarded-For"
		}
		if xff := r.Header.Get(header); xff != "" {
			if ip := firstForwardedIP(xff); ip != "" {
				return ip
			}
		}
	}
	return remoteAddrIP(r.RemoteAddr)
}

func firstForwardedIP(xff string) string {
	for _, part := range strings.Split(xff, ",") {
		candidate := strings.TrimSpace(part)
		if candidate == "" {
			continue
		}
		if ip := parseForwardedIPCandidate(candidate); ip != "" {
			return ip
		}
	}
	return ""
}

func parseForwardedIPCandidate(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	candidate = strings.Trim(candidate, "\"")
	if idx := strings.Index(candidate, ";"); idx >= 0 {
		candidate = candidate[:idx]
	}
	candidate = strings.TrimSpace(candidate)
	if strings.HasPrefix(strings.ToLower(candidate), "for=") {
		candidate = strings.TrimSpace(candidate[4:])
		candidate = strings.Trim(candidate, "\"")
	}
	if candidate == "" || strings.EqualFold(candidate, "unknown") {
		return ""
	}
	if ip := net.ParseIP(candidate); ip != nil {
		return ip.String()
	}
	if host, _, err := net.SplitHostPort(candidate); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	return ""
}

func remoteAddrIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.TrimSpace(addr)
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

func selectedHeaderAttrs(r *http.Request, headers []string) []loza.Attr {
	if len(headers) == 0 {
		return nil
	}
	attrs := make([]loza.Attr, 0, len(headers))
	for _, h := range headers {
		name := strings.TrimSpace(h)
		if name == "" {
			continue
		}
		// Header values may legitimately repeat (e.g. X-Forwarded-For). Capture
		// every value rather than only the first returned by Header.Get.
		rawValues := r.Header.Values(name)
		if len(rawValues) == 0 {
			continue
		}
		values := make([]string, 0, len(rawValues))
		for _, v := range rawValues {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				values = append(values, trimmed)
			}
		}
		if len(values) == 0 {
			continue
		}
		value := strings.Join(values, ", ")
		// normalize header name to attr key
		key := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
		// validate key (allow lowercase letters, numbers, dot, hyphen)
		if !isValidAttrKey(key) {
			// skip invalid header names
			continue
		}
		attrs = append(attrs, loza.String("http.header."+key, value))
	}
	return attrs
}

func isValidAttrKey(k string) bool {
	if len(k) == 0 || len(k) > 256 {
		return false
	}
	for _, ch := range k {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

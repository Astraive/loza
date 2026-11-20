package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	speccontract "github.com/astraive/loza/spec/generated/go/contract"
)

const (
	defaultTimeout      = 30 * time.Second
	maxCLIResponseBytes = 10 << 20
)

// RunCollectorCommand executes the local collector binary from the collector repo.
func RunCollectorCommand(ctx context.Context, collectorRepoPath string, args []string) error {
	return runGoCommand(ctx, collectorRepoPath, filepath.Join(".", "cmd", "loza-collector"), args)
}

// RunWorkerCommand executes the local worker binary from the collector repo.
func RunWorkerCommand(ctx context.Context, collectorRepoPath string, args []string) error {
	return runGoCommand(ctx, collectorRepoPath, filepath.Join(".", "cmd", "loza-worker"), args)
}

// RunLoadgenCommand executes the local load generator from the collector repo.
func RunLoadgenCommand(ctx context.Context, collectorRepoPath string, args []string) error {
	return runGoCommand(ctx, collectorRepoPath, filepath.Join(".", "cmd", "loza-loadgen"), args)
}

// CheckHealth checks the collector health endpoint.
func CheckHealth(baseURL string) error {
	return CheckHealthContext(context.Background(), baseURL)
}

// CheckHealthContext checks the collector health endpoint, honoring ctx.
func CheckHealthContext(ctx context.Context, baseURL string) error {
	if err := expectStatusContext(ctx, strings.TrimRight(baseURL, "/")+"/health", http.StatusOK); err == nil {
		return nil
	}
	return expectStatusContext(ctx, strings.TrimRight(baseURL, "/")+"/healthz", http.StatusOK)
}

// CheckReady checks the collector ready endpoint.
func CheckReady(baseURL string) error {
	return CheckReadyContext(context.Background(), baseURL)
}

// CheckReadyContext checks the collector ready endpoint, honoring ctx.
func CheckReadyContext(ctx context.Context, baseURL string) error {
	if err := expectStatusContext(ctx, strings.TrimRight(baseURL, "/")+"/ready", http.StatusOK); err == nil {
		return nil
	}
	return expectStatusContext(ctx, strings.TrimRight(baseURL, "/")+"/readyz", http.StatusOK)
}

// PostIngest posts event data to the collector ingest endpoint.
func PostIngest(baseURL, contentType string, payload []byte) error {
	return PostIngestContext(context.Background(), baseURL, contentType, payload)
}

// PostIngestContext posts event data to the collector ingest endpoint, honoring ctx.
func PostIngestContext(ctx context.Context, baseURL, contentType string, payload []byte) error {
	if err := validateIngestPayload(contentType, payload); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/events", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		if err := applyAPIKeyAuth(req); err != nil {
			return err
		}

		resp, err := clientWithTimeout(defaultTimeout).Do(req)
		if err != nil {
			lastErr = err
			if attempt < 2 {
				if sleepErr := sleepContext(ctx, backoffDelay(attempt, "")); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			break
		}

		body, readErr := readResponseBody(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt < 2 {
				if sleepErr := sleepContext(ctx, backoffDelay(attempt, "")); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			break
		}

		outcome, reason := collectorResponseOutcome(resp.StatusCode, body)
		switch outcome {
		case "success":
			return nil
		case "retryable":
			lastErr = fmt.Errorf("collector reported retryable response: %s", reason)
			if attempt < 2 {
				if sleepErr := sleepContext(ctx, backoffDelay(attempt, resp.Header.Get("Retry-After"))); sleepErr != nil {
					return sleepErr
				}
				continue
			}
			// Exhausted retries fall through so the post-loop return wraps lastErr.
		default:
			if resp.StatusCode >= 300 {
				return fmt.Errorf("collector returned %d: %s", resp.StatusCode, string(body))
			}
			return fmt.Errorf("collector rejected batch: %s", reason)
		}
	}
	return fmt.Errorf("collector send failed: %w", lastErr)
}

// FetchStatus fetches the collector status endpoint.
func FetchStatus(baseURL string) ([]byte, error) {
	return FetchStatusContext(context.Background(), baseURL)
}

// FetchStatusContext fetches the collector status endpoint, honoring ctx.
func FetchStatusContext(ctx context.Context, baseURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/status", nil)
	if err != nil {
		return nil, err
	}
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := readResponseBody(resp.Body)
		return nil, fmt.Errorf("status returned %d: %s", resp.StatusCode, string(body))
	}
	return readResponseBody(resp.Body)
}

// FetchMetrics fetches Prometheus metrics from the collector.
func FetchMetrics(baseURL string) ([]byte, error) {
	return FetchMetricsContext(context.Background(), baseURL)
}

// FetchMetricsContext fetches Prometheus metrics from the collector, honoring ctx.
func FetchMetricsContext(ctx context.Context, baseURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := readResponseBody(resp.Body)
		return nil, fmt.Errorf("metrics returned %d: %s", resp.StatusCode, string(body))
	}
	return readResponseBody(resp.Body)
}

// Query runs a query against the collector's query endpoint.
func Query(baseURL, engine, sqlQuery string) ([]byte, error) {
	payload := map[string]string{"engine": engine, "query": sqlQuery}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal query: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/query", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("query request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("query request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := readResponseBody(resp.Body)
		return nil, fmt.Errorf("query returned %d: %s", resp.StatusCode, string(body))
	}
	return readResponseBody(resp.Body)
}
func encodeLQLParameters(parameters map[string]any) map[string]any {
	encoded := make(map[string]any, len(parameters))
	for name, value := range parameters {
		if object, ok := value.(map[string]any); ok {
			if _, hasValue := object["value"]; hasValue {
				encoded[name] = object
				continue
			}
		}
		kind := "dynamic"
		switch value.(type) {
		case string:
			kind = "string"
		case bool:
			kind = "bool"
		case int, int8, int16, int32, int64:
			kind = "int"
		case json.Number:
			// Exact integer beyond int64, kept as a raw JSON number to avoid float rounding.
			kind = "int"
		case float32, float64:
			kind = "float"
		}
		encoded[name] = map[string]any{"type": kind, "value": value}
	}
	return encoded
}

// QueryLQL sends LQL source to the server-owned LQL query endpoint.
func QueryLQL(baseURL, source string, parameters map[string]any, limit int) ([]byte, error) {
	if limit <= 0 {
		limit = 1000
	} else if limit > 1000 {
		limit = 1000
	}
	payload := map[string]any{
		"query":      source,
		"parameters": encodeLQLParameters(parameters),
		"limit":      limit,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal lql query: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/lql/query", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("lql query request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("lql query request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := readResponseBody(resp.Body)
		return nil, fmt.Errorf("lql query returned %d: %s", resp.StatusCode, string(body))
	}
	return readResponseBody(resp.Body)
}

// TailStream opens an HTTP stream to the collector for tailing events.
func TailStream(ctx context.Context, baseURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/tail", nil)
	if err != nil {
		return nil, fmt.Errorf("tail request failed: %w", err)
	}
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("tail request failed: %w", err)
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("tail returned %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// ReplayEvents replays NDJSON events to the collector ingest endpoint.
func ReplayEvents(baseURL string, payload []byte) error {
	return PostIngest(baseURL, "application/x-ndjson", payload)
}

func FetchDLQ(baseURL string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"dlq"}, nil))
}

func FetchDLQItem(baseURL, id string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"dlq", id}, nil))
}

func ReplayDLQ(ctx context.Context, baseURL string) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"dlq", "replay"}, nil), nil)
}

func ReplayDLQItem(ctx context.Context, baseURL, id string) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"dlq", id, "replay"}, nil), nil)
}

func FetchSchema(baseURL string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"schema"}, nil))
}

func DiffSchema(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"schema", "diff"}, nil), payload)
}

func PublishSchema(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"schema", "publish"}, nil), payload)
}

func ValidateCollectorPayload(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"validate"}, nil), payload)
}

func CheckSchema(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"schema", "check"}, nil), payload)
}

func ValidatePolicy(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"policy", "validate"}, nil), payload)
}

func ApplyRetention(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"retention", "apply"}, nil), payload)
}

func AuditPII(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"audit", "pii"}, nil), payload)
}

func DeleteEventsByTenant(ctx context.Context, baseURL, tenantID, reason string) ([]byte, error) {
	return deleteJSON(ctx, buildURL(baseURL, []string{"events", "by-tenant", tenantID}, nil), reason)
}

func DeleteEventsByUser(ctx context.Context, baseURL, userID, reason string) ([]byte, error) {
	return deleteJSON(ctx, buildURL(baseURL, []string{"events", "by-user", userID}, nil), reason)
}

func DeleteEventByID(ctx context.Context, baseURL, eventID, reason string) ([]byte, error) {
	return deleteJSON(ctx, buildURL(baseURL, []string{"events", eventID}, nil), reason)
}

func FetchSinks(baseURL string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"sinks"}, nil))
}

func FetchSinkHealth(baseURL, name string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"sinks", name}, nil))
}

func TestSink(ctx context.Context, baseURL, name string) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"sinks", name, "test"}, nil), nil)
}

func DeleteDLQItem(ctx context.Context, baseURL, id string) ([]byte, error) {
	return deleteJSON(ctx, buildURL(baseURL, []string{"dlq", id}, nil), "")
}

func PublishBlueprint(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"schema", "blueprint"}, nil), payload)
}

func ListBlueprints(ctx context.Context, baseURL string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"schema", "blueprint"}, nil))
}

func FetchQuarantine(baseURL string) ([]byte, error) {
	return getJSON(buildURL(baseURL, []string{"quarantine"}, nil))
}

func ReplayQuarantineItem(ctx context.Context, baseURL, id string) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"quarantine", id, "replay"}, nil), nil)
}

func DeleteQuarantineItem(ctx context.Context, baseURL, id string) ([]byte, error) {
	return deleteJSON(ctx, buildURL(baseURL, []string{"quarantine", id}, nil), "")
}

func CreateAPIKey(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"keys"}, nil), payload)
}

func RevokeAPIKey(ctx context.Context, baseURL, id string) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"keys", id, "revoke"}, nil), nil)
}

func RotateAPIKey(ctx context.Context, baseURL, id string) ([]byte, error) {
	return postJSON(ctx, buildURL(baseURL, []string{"keys", id, "rotate"}, nil), nil)
}

func WatchStream(ctx context.Context, baseURL string, filters map[string]string) (io.ReadCloser, error) {
	target := buildURL(baseURL, []string{"tail"}, valuesFromStringMap(filters))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(0).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		body, _ := readResponseBody(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("watch returned %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

func expectStatus(url string, want int) error {
	return expectStatusContext(context.Background(), url, want)
}

func expectStatusContext(ctx context.Context, url string, want int) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if err := applyAPIKeyAuth(req); err != nil {
		return err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	return nil
}

func clientWithTimeout(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// sleepContext waits for d while remaining responsive to ctx cancellation.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func getJSON(rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("collector returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func postJSON(ctx context.Context, rawURL string, payload []byte) ([]byte, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("collector returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func deleteJSON(ctx context.Context, rawURL, reason string) ([]byte, error) {
	payload := []byte("{}")
	if strings.TrimSpace(reason) != "" {
		raw, err := json.Marshal(map[string]string{"reason": reason})
		if err != nil {
			return nil, err
		}
		payload = raw
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, rawURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyAPIKeyAuth(req); err != nil {
		return nil, err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("collector returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func validateIngestPayload(contentType string, payload []byte) error {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "ndjson") || !strings.Contains(ct, "json") {
		return nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return fmt.Errorf("collector ingest payload must be valid JSON: %w", err)
	}
	return validateIngestEnvelopeShape(envelope)
}

func readResponseBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxCLIResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCLIResponseBytes {
		return nil, fmt.Errorf("response too large: exceeds %d bytes", maxCLIResponseBytes)
	}
	return data, nil
}

func validateIngestEnvelopeShape(envelope map[string]any) error {
	if version, ok := envelope["api_version"].(string); !ok || strings.TrimSpace(version) == "" {
		return fmt.Errorf("collector envelope must include api_version")
	}
	source, ok := envelope["source"].(map[string]any)
	if !ok {
		return fmt.Errorf("collector envelope must include a source object")
	}
	for _, key := range []string{"sdk", "version", "service"} {
		value, ok := source[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("collector envelope source.%s must be a non-empty string", key)
		}
	}
	events, ok := envelope["events"].([]any)
	if !ok {
		return fmt.Errorf("collector envelope must include an events array")
	}
	if len(events) == 0 {
		return fmt.Errorf("collector envelope must include at least one event")
	}
	for idx, event := range events {
		if _, ok := event.(map[string]any); !ok {
			return fmt.Errorf("collector envelope events[%d] must be JSON objects", idx)
		}
	}
	return nil
}

func collectorResponseOutcome(status int, body []byte) (string, string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
			return "retryable", fmt.Sprintf("collector returned %d", status)
		}
		if status >= 300 {
			return "permanent", fmt.Sprintf("collector returned %d", status)
		}
		return "success", ""
	}

	resp, err := speccontract.ParseCollectorResponse(trimmed)
	if err != nil {
		if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
			return "retryable", fmt.Sprintf("collector response decode failed: %v", err)
		}
		if status >= 300 {
			return "permanent", fmt.Sprintf("collector response decode failed: %v", err)
		}
		return "success", ""
	}
	if retryable, reason := resp.RetryableError(); retryable {
		return "retryable", reason
	}
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return "retryable", collectorResponseSummary(resp)
	}
	if failed, reason := resp.PermanentFailure(); failed || status >= 300 {
		if strings.TrimSpace(reason) == "" {
			reason = collectorResponseSummary(resp)
		}
		return "permanent", reason
	}
	return "success", ""
}

func collectorResponseSummary(resp speccontract.CollectorResponse) string {
	if strings.TrimSpace(resp.Error) != "" {
		return resp.Error
	}
	if strings.TrimSpace(resp.Reason) != "" {
		return resp.Reason
	}
	return fmt.Sprintf("accepted=%d rejected=%d invalid=%d", resp.Accepted, resp.Rejected, resp.Invalid)
}

func backoffDelay(attempt int, retryAfter string) time.Duration {
	if delay := parseRetryAfter(retryAfter); delay > 0 {
		return delay
	}
	delay := time.Duration(math.Pow(2, float64(attempt))) * 50 * time.Millisecond
	if delay > time.Second {
		return time.Second
	}
	return delay
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if t, err := http.ParseTime(value); err == nil {
		if delay := time.Until(t); delay > 0 {
			return delay
		}
	}
	return 0
}

// applyAPIKeyAuth attaches the configured API key as a Bearer token. It is the
// single URL-validation gate for CLI requests: credentials are never sent over
// plaintext HTTP to a non-loopback host.
func applyAPIKeyAuth(req *http.Request) error {
	if req == nil || req.URL == nil {
		return nil
	}
	apiKey := getConfiguredAPIKey(req.URL.Host)
	if apiKey == "" {
		return nil
	}
	if req.URL.Scheme == "http" && !isLoopbackCLIHost(req.URL.Hostname()) {
		return fmt.Errorf("refusing to send API key over plaintext HTTP to non-local host %q", req.URL.Hostname())
	}
	// Never attach credentials to a host that was not explicitly configured.
	// This prevents a malicious --url/--collector-url (or config file) from
	// exfiltrating LOZA_API_KEY to an attacker-controlled endpoint.
	origin, ok := normalizeOrigin(req.URL.String())
	if !ok || !isAllowedOrigin(origin) {
		return nil
	}
	// Use Authorization: Bearer format (matches collector auth middleware)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return nil
}

func isLoopbackCLIHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// cfgAPIKey is an optional package-level API key set from loaded config.
// When non-empty, getConfiguredAPIKey uses it as a final fallback after env vars.
var cfgAPIKey string

// SetConfigAPIKey stores the API key from config so client functions can use it.
func SetConfigAPIKey(key string) {
	cfgAPIKey = strings.TrimSpace(key)
}

func getConfiguredAPIKey(host string) string {
	// Primary: LOZA_API_KEY (works for all services)
	if apiKey := strings.TrimSpace(os.Getenv("LOZA_API_KEY")); apiKey != "" {
		return apiKey
	}
	// Fallback: service-specific keys
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.Contains(host, "cortex") {
		if apiKey := strings.TrimSpace(os.Getenv("LOZA_CORTEX_API_KEY")); apiKey != "" {
			return apiKey
		}
		return cfgAPIKey
	}
	if apiKey := strings.TrimSpace(os.Getenv("LOZA_COLLECTOR_API_KEY")); apiKey != "" {
		return apiKey
	}
	return cfgAPIKey
}

func runGoCommand(ctx context.Context, repoPath, packagePath string, args []string) error {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return fmt.Errorf("repo path is not configured")
	}

	cmdArgs := []string{"run", packagePath}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "go", cmdArgs...)
	cmd.Dir = repoPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s: %w", filepath.Base(packagePath), err)
	}
	return nil
}

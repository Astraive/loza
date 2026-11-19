package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const defaultEventFetchTimeout = 15 * time.Second

// FetchEventsFromURL fetches an NDJSON array of events from a URL supplied via
// the cortex ingest --url flag. The origin must be in the configured allowlist
// (SSRF protection), and the request is bounded by both a context and a client
// timeout. Non-2xx responses are surfaced as errors rather than silently
// treated as "no events".
func FetchEventsFromURL(ctx context.Context, rawURL string) ([]map[string]any, error) {
	if !IsAllowedURL(rawURL) {
		return nil, fmt.Errorf("refusing to fetch events from %q: host is not in the configured allowlist", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch events request: %w", err)
	}
	httpClient := &http.Client{Timeout: defaultEventFetchTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch events: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch events returned status %d", resp.StatusCode)
	}
	var events []map[string]any
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxCLIResponseBytes+1))
	if err := decoder.Decode(&events); err != nil {
		return nil, fmt.Errorf("decode fetched events: %w", err)
	}
	return events, nil
}

// RunCortexCommand executes the local cortex binary from the cortex repo.
func RunCortexCommand(ctx context.Context, cortexRepoPath string, args []string) error {
	return runGoCommand(ctx, cortexRepoPath, filepath.Join(".", "cmd", "server"), args)
}

// CheckCortexHealth checks the cortex health endpoint.
func CheckCortexHealth(baseURL string) error {
	return CheckCortexHealthContext(context.Background(), baseURL)
}

// CheckCortexHealthContext checks the cortex health endpoint, honoring ctx.
func CheckCortexHealthContext(ctx context.Context, baseURL string) error {
	return expectStatusContext(ctx, strings.TrimRight(baseURL, "/")+"/healthz", http.StatusOK)
}

// CheckCortexReady checks the cortex ready endpoint.
func CheckCortexReady(baseURL string) error {
	return CheckCortexReadyContext(context.Background(), baseURL)
}

// CheckCortexReadyContext checks the cortex ready endpoint, honoring ctx.
func CheckCortexReadyContext(ctx context.Context, baseURL string) error {
	return expectStatusContext(ctx, strings.TrimRight(baseURL, "/")+"/readyz", http.StatusOK)
}

// FetchCortexMetrics fetches Prometheus metrics from cortex.
func FetchCortexMetrics(baseURL string) ([]byte, error) {
	return FetchCortexMetricsContext(context.Background(), baseURL)
}

// FetchCortexMetricsContext fetches Prometheus metrics from cortex, honoring ctx.
func FetchCortexMetricsContext(ctx context.Context, baseURL string) ([]byte, error) {
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
	body, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cortex returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// IngestCortexBatch sends a batch of events to cortex.
func IngestCortexBatch(ctx context.Context, baseURL string, events []map[string]any) error {
	raw, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/events/batch", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := applyAPIKeyAuth(req); err != nil {
		return err
	}
	resp, err := clientWithTimeout(defaultTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := readResponseBody(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cortex returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func ReconstructCortexIncident(ctx context.Context, baseURL, incidentID, mode string, limit int) ([]byte, error) {
	payload := map[string]any{
		"incident_id": incidentID,
	}
	if strings.TrimSpace(mode) != "" {
		payload["mode"] = mode
	}
	if limit > 0 {
		payload["limit"] = limit
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return postCortexJSON(ctx, baseURL, "/reconstruct", raw)
}

func RecordCortexRemediation(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postCortexJSON(ctx, baseURL, "/feedback/remediation", payload)
}

func RecordCortexFeedback(ctx context.Context, baseURL string, payload []byte) ([]byte, error) {
	return postCortexJSON(ctx, baseURL, "/feedback/incident", payload)
}

func FetchCortexServiceGraph(ctx context.Context, baseURL, service string, depth int) ([]byte, error) {
	path := "/graph/service/" + url.PathEscape(service) + "?depth=" + url.QueryEscape(fmt.Sprint(clampDepth(depth)))
	return getCortexJSON(ctx, baseURL, path)
}

func FetchCortexIncidentGraph(ctx context.Context, baseURL, incidentID string, depth int) ([]byte, error) {
	path := "/graph/incident/" + url.PathEscape(incidentID) + "?depth=" + url.QueryEscape(fmt.Sprint(clampDepth(depth)))
	return getCortexJSON(ctx, baseURL, path)
}

func getCortexJSON(ctx context.Context, baseURL, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
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
		return nil, fmt.Errorf("cortex returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func QueryCortexGraphQL(ctx context.Context, baseURL, query string, variables map[string]any) ([]byte, error) {
	payload := map[string]any{"query": query}
	if variables != nil {
		payload["variables"] = variables
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return postCortexJSON(ctx, baseURL, "/graphql", raw)
}

func FetchSignatures(ctx context.Context, baseURL string, limit int) ([]byte, error) {
	query := fmt.Sprintf(`{ signatures(limit: %d) { id pattern count lastSeen } }`, clampLimit(limit, 100, 1000))
	return QueryCortexGraphQL(ctx, baseURL, query, nil)
}

func clampDepth(depth int) int {
	if depth < 1 {
		return 1
	}
	if depth > 10 {
		return 10
	}
	return depth
}

func clampLimit(limit, fallback, max int) int {
	if limit < 1 {
		return fallback
	}
	if limit > max {
		return max
	}
	return limit
}

func postCortexJSON(ctx context.Context, baseURL, path string, payload []byte) ([]byte, error) {
	if payload == nil {
		payload = []byte("{}")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(payload))
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
		return nil, fmt.Errorf("cortex returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

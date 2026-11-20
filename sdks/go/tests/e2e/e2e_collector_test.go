package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	loza "github.com/astraive/loza/sdks/go"
)

const defaultCollectorURL = "http://127.0.0.1:9308"

// collectorRequestTimeout bounds every HTTP probe made against the collector
// so a stalled or unresponsive collector fails the E2E test instead of hanging
// indefinitely. A variable so the regression test can shorten it.
var collectorRequestTimeout = 10 * time.Second

// collectorHTTPClient returns a client with a finite timeout for collector
// probes. Go's default client has a zero timeout, which can hang the suite.
func collectorHTTPClient() *http.Client {
	return &http.Client{Timeout: collectorRequestTimeout}
}

// fataler is the subset of testing.TB used by readResponseBody. Keeping it as
// an interface lets the read-error handling be unit tested without a live
// collector.
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// readResponseBody reads the entire response body and fails the test when the
// read itself fails, so callers never act on a possibly truncated body.
func readResponseBody(t fataler, r io.Reader) []byte {
	t.Helper()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func collectorURL() string {
	if value := strings.TrimRight(os.Getenv("LOZA_TEST_COLLECTOR_URL"), "/"); value != "" {
		return value
	}
	return defaultCollectorURL
}

func ingestAPIKey() string {
	return os.Getenv("LOZA_API_KEY")
}

func adminAPIKey() string {
	return os.Getenv("LOZA_TEST_COLLECTOR_ADMIN_KEY")
}

// newTestClient creates a SDK Logger that auto-installs HTTPBatchSink
// when CollectorURL is set — this is the production path.
func newTestClient(t *testing.T, opts ...loza.ConfigOption) *loza.Logger {
	t.Helper()
	cfg := loza.Config{
		Service:      "e2e-test",
		CollectorURL: collectorURL(),
		APIKey:       ingestAPIKey(),
	}
	for _, opt := range opts {
		cfg = opt(cfg)
	}
	l, err := loza.NewClient(cfg)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { l.Shutdown(context.Background()) })
	return l
}

func TestE2E_HTTPBatchSink_BasicEvent(t *testing.T) {
	l := newTestClient(t)

	ctx := context.Background()
	l.StartEvent(ctx, loza.Params{Event: "e2e.batch.basic"})
	loza.Set(ctx, loza.String("test_id", "batch-001"))
	loza.Set(ctx, loza.String("env", "e2e"))
	if err := loza.Finish(ctx, "success"); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if err := l.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	body := getCollectorStatus(t)
	t.Logf("Collector status: %s", string(body))
}

func TestE2E_HTTPBatchSink_WithRedaction(t *testing.T) {
	l := newTestClient(t, loza.WithRedactor(loza.DefaultRedactor()))

	ctx := context.Background()
	l.StartEvent(ctx, loza.Params{Event: "e2e.batch.redaction"})
	loza.Set(ctx, loza.String("password", "supersecret"))
	loza.Set(ctx, loza.String("user_input", "normal value"))
	if err := loza.Finish(ctx, "success"); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if err := l.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	time.Sleep(500 * time.Millisecond)
	getCollectorStatus(t)
}

func TestE2E_HTTPBatchSink_AsyncBatch(t *testing.T) {
	l := newTestClient(t,
		loza.WithAsync(true),
		loza.WithWorkers(2),
	)

	ctx := context.Background()
	for i := range 10 {
		l.StartEvent(ctx, loza.Params{Event: "e2e.batch.async"})
		loza.Set(ctx, loza.Int("seq", i))
		if err := loza.Finish(ctx, "success"); err != nil {
			t.Fatalf("finish %d: %v", i, err)
		}
	}

	if err := l.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	time.Sleep(1 * time.Second)
	getCollectorStatus(t)
}

func TestE2E_HTTPBatchSink_VersionCheck(t *testing.T) {
	_ = newTestClient(t)

	version, err := fetchCollectorVersion(t, collectorHTTPClient(), collectorURL())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	t.Logf("Collector version: %+v", version)

	if _, ok := version["ingest_api_version"]; !ok {
		t.Fatal("expected ingest_api_version in version response")
	}
}

// fetchCollectorVersion performs the /version probe with the supplied client
// so the request's timeout behavior can be exercised in isolation.
func fetchCollectorVersion(t fataler, client *http.Client, baseURL string) (map[string]any, error) {
	resp, err := client.Get(baseURL + "/version")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body := readResponseBody(t, resp.Body)
	var version map[string]any
	if err := json.Unmarshal(body, &version); err != nil {
		return nil, err
	}
	return version, nil
}

func TestE2E_HTTPBatchSink_MultipleFlushes(t *testing.T) {
	l := newTestClient(t)

	ctx := context.Background()
	for batch := range 3 {
		for i := range 5 {
			l.StartEvent(ctx, loza.Params{Event: "e2e.batch.multi"})
			loza.Set(ctx, loza.Int("batch", batch))
			loza.Set(ctx, loza.Int("seq", i))
			if err := loza.Finish(ctx, "success"); err != nil {
				t.Fatalf("finish batch=%d seq=%d: %v", batch, i, err)
			}
		}
		if err := l.Flush(ctx); err != nil {
			t.Fatalf("flush batch %d: %v", batch, err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)
	body := getCollectorStatus(t)
	t.Logf("After 3 batches of 5: %s", string(body))
}

func getCollectorStatus(t *testing.T) []byte {
	t.Helper()
	body, err := fetchCollectorStatus(t, collectorHTTPClient(), collectorURL(), adminAPIKey())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return body
}

// fetchCollectorStatus performs the /status probe with the supplied client so
// the request's timeout behavior can be exercised in isolation.
func fetchCollectorStatus(t fataler, client *http.Client, baseURL, token string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/status", nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body := readResponseBody(t, resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// TestCollectorHTTPRequestsAreBounded is a regression test for the default
// client's zero timeout: a collector that accepts a connection but never
// answers must not hang the E2E suite forever.
func TestCollectorHTTPRequestsAreBounded(t *testing.T) {
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer stalled.Close()

	originalTimeout := collectorRequestTimeout
	collectorRequestTimeout = 100 * time.Millisecond
	defer func() { collectorRequestTimeout = originalTimeout }()

	client := collectorHTTPClient()
	if client.Timeout <= 0 {
		t.Fatalf("expected a finite client timeout, got %s", client.Timeout)
	}

	start := time.Now()
	if _, err := fetchCollectorVersion(t, client, stalled.URL); err == nil {
		t.Fatal("expected /version request against a stalled collector to fail")
	}
	if _, err := fetchCollectorStatus(t, client, stalled.URL, ""); err == nil {
		t.Fatal("expected /status request against a stalled collector to fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stalled collector requests took %s; expected them to be bounded by the client timeout", elapsed)
	}
}

// recordingFataler captures Fatalf calls so readResponseBody can be exercised
// without a real *testing.T (which would abort the goroutine).
type recordingFataler struct {
	fatal   bool
	message string
}

func (r *recordingFataler) Helper() {}

func (r *recordingFataler) Fatalf(format string, args ...any) {
	r.fatal = true
	r.message = fmt.Sprintf(format, args...)
}

// errReadBody is the sentinel read error used by the regression tests.
var errReadBody = errors.New("simulated read failure")

// failingBodyReader yields a few bytes and then fails, mimicking a truncated
// response body where io.ReadAll surfaces an error alongside partial data.
type failingBodyReader struct {
	remaining int
}

func (r *failingBodyReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errReadBody
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 'x'
	}
	r.remaining -= len(p)
	return len(p), nil
}

// Regression test for MASS-0524: a body read failure must be surfaced rather
// than silently discarded, so callers never act on a truncated body.
func TestReadResponseBody_SurfacesReadError(t *testing.T) {
	f := &recordingFataler{}
	got := readResponseBody(f, &failingBodyReader{remaining: 4})
	if !f.fatal {
		t.Fatal("expected the read failure to be reported via Fatalf")
	}
	if !strings.Contains(f.message, errReadBody.Error()) {
		t.Fatalf("expected read error in Fatalf message, got %q", f.message)
	}
	if string(got) != "xxxx" {
		t.Fatalf("expected the bytes read before the failure, got %q", got)
	}
}

func TestReadResponseBody_Success(t *testing.T) {
	f := &recordingFataler{}
	got := readResponseBody(f, strings.NewReader("ok"))
	if f.fatal {
		t.Fatalf("unexpected Fatalf: %s", f.message)
	}
	if string(got) != "ok" {
		t.Fatalf("got %q, want %q", got, "ok")
	}
}

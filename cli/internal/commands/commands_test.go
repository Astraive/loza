package commands

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
	"github.com/astraive/loza/cli/internal/output"
)

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	err := run()
	_ = w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return string(buf[:n]), err
}

func captureStderr(t *testing.T, run func() error) (string, error) {
	t.Helper()
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() { os.Stderr = old }()

	err := run()
	_ = w.Close()
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	return string(buf[:n]), err
}

func TestEmitSamplePrintsEnvelope(t *testing.T) {
	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	err := EmitCommand(cfg, []string{"sample", "--service", "checkout", "--event", "payment.completed", "--print"})
	_ = w.Close()
	if err != nil {
		t.Fatalf("emit sample: %v", err)
	}
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, `"event": "payment.completed"`) {
		t.Fatalf("expected printed sample event, got %s", out)
	}
}

func TestEmitSampleWritesOutputFile(t *testing.T) {
	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	path := filepath.Join(t.TempDir(), "sample.json")
	if err := EmitCommand(cfg, []string{"sample", "--output", path, "--print"}); err != nil {
		t.Fatalf("emit sample: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(raw), `"api_version": "v1"`) {
		t.Fatalf("unexpected output file: %s", string(raw))
	}
}

func TestDLQListHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dlq" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"events":[],"count":0}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := DLQCommand(context.Background(), cfg, []string{"list"}); err != nil {
		t.Fatalf("dlq list: %v", err)
	}
}

func TestDLQCommandsEscapeIDInRequestPath(t *testing.T) {
	tests := []struct {
		subcommand string
		method     string
		suffix     string
	}{
		{"show", http.MethodGet, ""},
		{"delete", http.MethodDelete, ""},
		{"replay", http.MethodPost, "/replay"},
	}
	id := "../../events/by-tenant/x"

	for _, tc := range tests {
		t.Run(tc.subcommand, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.EscapedPath()
				if r.Method != tc.method {
					t.Errorf("method = %s, want %s", r.Method, tc.method)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			cfg := config.Config{CollectorURL: srv.URL}
			if _, err := captureStdout(t, func() error {
				return DLQCommand(context.Background(), cfg, []string{tc.subcommand, id})
			}); err != nil {
				t.Fatalf("dlq %s: %v", tc.subcommand, err)
			}

			want := "/dlq/" + url.PathEscape(id) + tc.suffix
			if gotPath != want {
				t.Fatalf("path = %q, want %q", gotPath, want)
			}
		})
	}
}

func TestEmitSampleSendsAPIKeyFromEnv(t *testing.T) {
	t.Setenv("LOZA_API_KEY", "test-key")

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := EmitCommand(cfg, []string{"sample", "--service", "checkout", "--event", "payment.completed"}); err != nil {
		t.Fatalf("emit sample: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("expected Authorization: Bearer header, got %q", gotAuth)
	}
}

func TestSchemaListHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/schema" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"registry":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := SchemaCommand(cfg, []string{"list"}); err != nil {
		t.Fatalf("schema list: %v", err)
	}
}

func TestSchemaFetchValidatesLocalSpecRepo(t *testing.T) {
	specRepo, err := filepath.Abs(filepath.Join("..", "..", "..", "spec"))
	if err != nil {
		t.Fatalf("resolve spec repo path: %v", err)
	}
	if err := SchemaCommand(config.Config{SpecRepoPath: specRepo}, []string{"fetch"}); err != nil {
		t.Fatalf("schema fetch on a complete spec repo: %v", err)
	}
	if err := SchemaCommand(config.Config{SpecRepoPath: t.TempDir()}, []string{"fetch"}); err == nil {
		t.Fatal("expected schema fetch to reject a spec repo with no assets")
	}
}

func TestAuditPIIHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audit/pii" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"count":0,"findings":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := AuditCommand(context.Background(), cfg, []string{"pii"}); err != nil {
		t.Fatalf("audit pii: %v", err)
	}
}

func TestDeployComposeCopiesCollectorAssets(t *testing.T) {
	collectorRepo := filepath.Join(t.TempDir(), "collector")
	deployDir := filepath.Join(collectorRepo, "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy dir: %v", err)
	}
	src := filepath.Join(deployDir, "docker-compose.yml")
	if err := os.WriteFile(src, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write compose asset: %v", err)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	cfg := config.Config{CollectorRepoPath: collectorRepo}
	if err := DeployCommand(cfg, []string{"compose", "--out", outDir}); err != nil {
		t.Fatalf("deploy compose: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "docker-compose.yml")); err != nil {
		t.Fatalf("expected copied compose file: %v", err)
	}
}

func TestDashboardInstallCopiesCollectorAssets(t *testing.T) {
	collectorRepo := filepath.Join(t.TempDir(), "collector")
	dashboardDir := filepath.Join(collectorRepo, "deploy", "observability", "grafana", "provisioning", "dashboards")
	if err := os.MkdirAll(dashboardDir, 0o755); err != nil {
		t.Fatalf("mkdir dashboard dir: %v", err)
	}
	src := filepath.Join(dashboardDir, "loza-collector.json")
	if err := os.WriteFile(src, []byte(`{"title":"LOZA Collector"}`), 0o644); err != nil {
		t.Fatalf("write dashboard asset: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	cfg := config.Config{CollectorRepoPath: collectorRepo, CollectorURL: "http://127.0.0.1:65535"}
	if err := DashboardCommand(cfg, []string{"install"}); err != nil {
		t.Fatalf("dashboard install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".loza-dashboard", "dashboards", "loza-collector.json")); err != nil {
		t.Fatalf("expected copied dashboard asset: %v", err)
	}
}

func TestDoctorChecksCortexWhenConfigured(t *testing.T) {
	collectorRepo := t.TempDir()
	specRepo := t.TempDir()
	cortexRepo := t.TempDir()

	collectorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health", "/ready":
			w.WriteHeader(http.StatusOK)
		case "/status":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/events":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"status":"accepted","accepted":1}`))
		case "/metrics":
			_, _ = w.Write([]byte("loza_collector_events_accepted_total 1\n"))
		default:
			t.Fatalf("unexpected collector path %s", r.URL.Path)
		}
	}))
	defer collectorSrv.Close()

	cortexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz", "/readyz":
			w.WriteHeader(http.StatusOK)
		case "/metrics":
			_, _ = w.Write([]byte("loza_cortex_events_ingested_total 1\n"))
		default:
			t.Fatalf("unexpected cortex path %s", r.URL.Path)
		}
	}))
	defer cortexSrv.Close()

	dbPath := filepath.Join(t.TempDir(), "doctor.db")
	cfg := config.Config{
		CollectorRepoPath: collectorRepo,
		CortexRepoPath:    cortexRepo,
		SpecRepoPath:      specRepo,
		CollectorURL:      collectorSrv.URL,
		DuckDBPath:        dbPath,
		Cortex:            &config.CortexConfig{URL: cortexSrv.URL},
	}
	if err := DoctorCommand(context.Background(), cfg, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
}

func TestRunCortexServerRequiresConfiguredRepo(t *testing.T) {
	cfg := config.Config{}
	err := RunCortexServer(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "cortex_repo_path") {
		t.Fatalf("expected cortex repo path error, got %v", err)
	}
}

func TestRunCortexIngestRejectsBothFileAndURL(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "events.ndjson")
	if err := os.WriteFile(filePath, []byte(`{"event":"from.file"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write events file: %v", err)
	}

	var urlHits atomic.Int32
	urlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlHits.Add(1)
		_, _ = w.Write([]byte(`[{"event":"from.url"}]`))
	}))
	defer urlSrv.Close()

	var ingested atomic.Int32
	cortexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events/batch" {
			ingested.Add(1)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer cortexSrv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: cortexSrv.URL}}
	err := RunCortexIngest(context.Background(), cfg, []string{"--file", filePath, "--url", urlSrv.URL})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected mutually exclusive error, got %v", err)
	}
	if urlHits.Load() != 0 {
		t.Fatalf("URL should not be fetched when both sources are given, got %d hits", urlHits.Load())
	}
	if ingested.Load() != 0 {
		t.Fatalf("no events should be ingested when both sources are given, got %d", ingested.Load())
	}
}

func TestRunCortexReconstructHitsSharedClientPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reconstruct" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"incident_id":"inc_1","timestamp":"2026-05-17T00:00:00Z","confidence":0.9,"causal_chain":[],"similar_incidents":[],"symptoms":[],"suggested_actions":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
	if err := RunCortexReconstruct(context.Background(), cfg, []string{"--incident", "inc_1"}); err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
}

func TestRunCortexGraphHitsSharedClientPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/graph/service/checkout") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"nodes":[{"id":"n1"}],"edges":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
	if err := RunCortexGraph(context.Background(), cfg, []string{"--service", "checkout"}); err != nil {
		t.Fatalf("graph: %v", err)
	}
}

func TestRunCortexGraphMalformedResponseReturnsError(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "error shape",
			body: `{"error":"something went wrong"}`,
			want: "'nodes'",
		},
		{
			name: "missing fields",
			body: `{}`,
			want: "'nodes'",
		},
		{
			name: "nodes not an array",
			body: `{"nodes":{"id":"n1"},"edges":[]}`,
			want: "'nodes'",
		},
		{
			name: "edges not an array",
			body: `{"nodes":[],"edges":"bad"}`,
			want: "'edges'",
		},
		{
			name: "missing edges",
			body: `{"nodes":[]}`,
			want: "'edges'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
			err := RunCortexGraph(context.Background(), cfg, []string{"--service", "checkout"})
			if err == nil {
				t.Fatalf("expected an error for body %s, got nil", tt.body)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestStatusHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok","uptime":"5m","events_accepted":100}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := StatusCommand(context.Background(), cfg, nil); err != nil {
		t.Fatalf("status: %v", err)
	}
}

func TestSinksListHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sinks" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"sinks":[{"name":"duckdb","type":"primary","health":"ok"}]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := SinksCommand(context.Background(), cfg, []string{"list"}); err != nil {
		t.Fatalf("sinks list: %v", err)
	}
}

func TestSinksEmptyFirstArgumentDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sinks" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"sinks":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	// An empty first argument must not panic on args[0][0]; it falls through to
	// the default "list" subcommand just like a leading flag does.
	if err := SinksCommand(context.Background(), cfg, []string{""}); err != nil {
		t.Fatalf("sinks with empty first argument: %v", err)
	}
}

func TestSinksTestHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sinks/duckdb/test" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"status":"ok","message":"sink test passed"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := SinksCommand(context.Background(), cfg, []string{"test", "duckdb"}); err != nil {
		t.Fatalf("sinks test: %v", err)
	}
}

func TestSinksTestJSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","message":"sink test passed"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	ctx := output.WithFormat(context.Background(), "json")
	out, err := captureStdout(t, func() error {
		return SinksCommand(ctx, cfg, []string{"test", "duckdb"})
	})
	if err != nil {
		t.Fatalf("sinks test json: %v", err)
	}
	if !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("expected raw json output, got %s", out)
	}
}

func TestSinksTestTextOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","message":"sink test passed"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	out, err := captureStdout(t, func() error {
		return SinksCommand(context.Background(), cfg, []string{"test", "duckdb"})
	})
	if err != nil {
		t.Fatalf("sinks test text: %v", err)
	}
	if !strings.Contains(out, "Sink test: duckdb") {
		t.Fatalf("expected sink test section, got %s", out)
	}
	if !strings.Contains(out, "sink test passed") {
		t.Fatalf("expected readable sink test output, got %s", out)
	}
}

func TestIncidentCommandWarnsWhenGraphFetchFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/reconstruct":
			_, _ = w.Write([]byte(`{"incident_id":"inc_1","timestamp":"2026-05-17T00:00:00Z","confidence":0.9,"primary_service":"checkout","severity":"high","related_services":[],"causal_chain":[],"symptoms":[],"suggested_remediations":[]}`))
		case strings.HasPrefix(r.URL.Path, "/graph/incident/"):
			http.Error(w, "graph backend unavailable", http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
	stderr, err := captureStderr(t, func() error {
		return IncidentCommand(context.Background(), cfg, []string{"inc_1"})
	})
	if err != nil {
		t.Fatalf("incident command should still succeed when only the graph fetch fails: %v", err)
	}
	if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, "graph") {
		t.Fatalf("expected a stderr warning about the missing dependency graph, got %q", stderr)
	}
	if !strings.Contains(stderr, "500") {
		t.Fatalf("expected the underlying fetch error to be surfaced, got %q", stderr)
	}
}

func TestIncidentCommandRendersGraphOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/reconstruct":
			_, _ = w.Write([]byte(`{"incident_id":"inc_1","timestamp":"2026-05-17T00:00:00Z","confidence":0.9,"primary_service":"checkout","severity":"high","related_services":[],"causal_chain":[],"symptoms":[],"suggested_remediations":[]}`))
		case strings.HasPrefix(r.URL.Path, "/graph/incident/"):
			_, _ = w.Write([]byte(`{"nodes":[{"id":"n1","label":"checkout","type":"service"}],"edges":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
	var stderr string
	out, err := captureStdout(t, func() error {
		var captureErr error
		stderr, captureErr = captureStderr(t, func() error {
			return IncidentCommand(context.Background(), cfg, []string{"inc_1"})
		})
		return captureErr
	})
	if err != nil {
		t.Fatalf("incident command: %v", err)
	}
	if !strings.Contains(out, "Dependency Graph:") || !strings.Contains(out, "checkout") {
		t.Fatalf("expected rendered dependency graph, got %s", out)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr warning on successful graph fetch: %q", stderr)
	}
}

func TestSinksShowHitsCollectorAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sinks/duckdb" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"name":"duckdb","status":"healthy"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := SinksCommand(context.Background(), cfg, []string{"show", "duckdb"}); err != nil {
		t.Fatalf("sinks show: %v", err)
	}
}

func TestSinksRejectsUnsafeNames(t *testing.T) {
	requested := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = true
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	unsafeNames := []string{
		"../admin",
		"a/b",
		"..",
		"duckdb?verbose=1",
		"duckdb#fragment",
		"duckdb extra",
		"duckdb%2Fadmin",
		"",
	}
	for _, name := range unsafeNames {
		for _, sub := range []string{"show", "test"} {
			err := SinksCommand(context.Background(), cfg, []string{sub, name})
			if err == nil {
				t.Errorf("expected %s %q to be rejected", sub, name)
				continue
			}
			if !strings.Contains(err.Error(), "invalid sink name") {
				t.Errorf("expected invalid sink name error for %s %q, got %v", sub, name, err)
			}
		}
	}
	if requested {
		t.Fatal("no collector request should be made for an invalid sink name")
	}
}

func TestSinksAcceptsSafeNames(t *testing.T) {
	for _, name := range []string{"duckdb", "primary-1", "sink_v2", "sink.v2"} {
		if err := validateSinkName(name); err != nil {
			t.Errorf("expected %q to be a valid sink name, got %v", name, err)
		}
	}
}

func TestGraphServiceHitsCortexAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/graph/service/") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"nodes":[{"id":"n1","label":"checkout","type":"service"}],"edges":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
	if err := GraphCommand(context.Background(), cfg, []string{"service", "checkout"}); err != nil {
		t.Fatalf("graph: %v", err)
	}
}

func TestDebugEventQueriesCollector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"columns":["event_id","event"],"rows":[["evt_1","test.event"]]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := DebugCommand(context.Background(), cfg, []string{"event", "evt_1"}); err != nil {
		t.Fatalf("debug event: %v", err)
	}
}

func TestDebugPipelineShowsStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/sinks":
			_, _ = w.Write([]byte(`{"sinks":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := DebugCommand(context.Background(), cfg, []string{"pipeline"}); err != nil {
		t.Fatalf("debug pipeline: %v", err)
	}
}

func TestExportAcceptsOutputFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"columns":["event_id"],"rows":[["evt_1"]]}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "events.json")
	cfg := config.Config{CollectorURL: srv.URL}
	if err := ExportCommand(cfg, []string{"--output", path}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected export output file at %s: %v", path, err)
	}
}

func TestCollectorHealthURLUsesConfiguredEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		collectorURL string
		want         string
	}{
		{
			name:         "non-default port",
			collectorURL: "http://127.0.0.1:9451",
			want:         "http://127.0.0.1:9451/health",
		},
		{
			name:         "custom host and port",
			collectorURL: "http://collector.internal:8080",
			want:         "http://collector.internal:8080/health",
		},
		{
			name:         "trailing slash is normalized",
			collectorURL: "http://127.0.0.1:9451/",
			want:         "http://127.0.0.1:9451/health",
		},
		{
			name:         "empty URL falls back to default port",
			collectorURL: "",
			want:         "http://localhost:9308/health",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectorHealthURL(config.Config{CollectorURL: tt.collectorURL})
			if got != tt.want {
				t.Fatalf("collectorHealthURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSchemaValidateStrictRequiresFileArgument(t *testing.T) {
	cfg := config.Config{SpecRepoPath: t.TempDir()}

	_, err := captureStdout(t, func() error {
		return SchemaCommand(cfg, []string{"validate", "--strict"})
	})
	if err == nil {
		t.Fatal("expected --strict with no file argument to fail")
	}
	if !strings.Contains(err.Error(), "--strict") || !strings.Contains(err.Error(), "file argument") {
		t.Fatalf("expected error explaining --strict requires a file argument, got %v", err)
	}
}

func TestSchemaValidateNoPathWithoutStrictChecksSpecAssets(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cfg := config.Config{SpecRepoPath: filepath.Clean(filepath.Join(wd, "..", "..", "..", "spec"))}

	out, err := captureStdout(t, func() error {
		return SchemaCommand(cfg, []string{"validate"})
	})
	if err != nil {
		t.Fatalf("schema validate without strict: %v", err)
	}
	if !strings.Contains(out, "Spec assets validated successfully") {
		t.Fatalf("expected spec-asset validation success message, got %s", out)
	}
}

// TestCommandsRejectPathInjectionIDs verifies the call sites named in
// MASS-0035 reject IDs that could inject URL path segments or a query string,
// and that no request is sent for such input.
func TestCommandsRejectPathInjectionIDs(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	ctx := context.Background()
	const maliciousID = "x/../../keys?force=true"

	tests := []struct {
		name string
		run  func() error
	}{
		{"keys revoke", func() error { return KeysCommand(ctx, cfg, []string{"revoke", maliciousID}) }},
		{"keys rotate", func() error { return KeysCommand(ctx, cfg, []string{"rotate", maliciousID}) }},
		{"quarantine replay", func() error { return QuarantineCommand(ctx, cfg, []string{"replay", maliciousID}) }},
		{"quarantine delete", func() error { return QuarantineCommand(ctx, cfg, []string{"delete", maliciousID}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "invalid id") {
				t.Fatalf("expected invalid id error, got %v", err)
			}
		})
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("expected no HTTP requests for injected IDs, got %d", got)
	}
}

func TestEmitWithCustomKindAndOutcome(t *testing.T) {
	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	err := EmitCommand(cfg, []string{"sample", "--kind", "http", "--outcome", "error", "--level", "warn", "--print"})
	_ = w.Close()
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, `"kind": "http"`) {
		t.Fatalf("expected kind=http in output, got %s", out)
	}
	if !strings.Contains(out, `"outcome": "error"`) {
		t.Fatalf("expected outcome=error in output, got %s", out)
	}
}

func TestSignaturesCommandHandlesUnexpectedJSONShape(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"data_missing", `{"other":1}`},
		{"data_null", `{"data":null}`},
		{"data_not_object", `{"data":[]}`},
		{"data_string", `{"data":"unexpected"}`},
		{"null_body", `null`},
		{"signatures_missing", `{"data":{}}`},
		{"signatures_not_array", `{"data":{"signatures":"unexpected"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
			var out string
			var err error
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						t.Fatalf("SignaturesCommand panicked on %s: %v", tt.body, rec)
					}
				}()
				out, err = captureStdout(t, func() error {
					return SignaturesCommand(context.Background(), cfg, nil)
				})
			}()
			if err != nil {
				t.Fatalf("signatures: %v", err)
			}
			if !strings.Contains(out, tt.body) {
				t.Fatalf("expected raw body %q in output, got %q", tt.body, out)
			}
		})
	}
}

func TestOutputIncidentContextPrintsValidSuccessRate(t *testing.T) {
	ctx := map[string]interface{}{
		"suggested_actions": []interface{}{
			map[string]interface{}{"action": "restart", "success_rate": 0.85},
		},
	}
	out, err := captureStdout(t, func() error {
		outputIncidentContext(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("outputIncidentContext: %v", err)
	}
	if !strings.Contains(out, "  - restart (success rate: 85.0%)") {
		t.Fatalf("expected formatted success rate in output, got %q", out)
	}
}

func TestOutputIncidentContextHandlesMissingOrInvalidSuccessRate(t *testing.T) {
	tests := []struct {
		name   string
		action map[string]interface{}
	}{
		{
			name:   "missing success_rate",
			action: map[string]interface{}{"action": "restart"},
		},
		{
			name:   "string success_rate",
			action: map[string]interface{}{"action": "restart", "success_rate": "0.85"},
		},
		{
			name:   "null success_rate",
			action: map[string]interface{}{"action": "restart", "success_rate": nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := map[string]interface{}{
				"suggested_actions": []interface{}{tt.action},
			}
			out, err := captureStdout(t, func() error {
				outputIncidentContext(ctx)
				return nil
			})
			if err != nil {
				t.Fatalf("outputIncidentContext: %v", err)
			}
			if !strings.Contains(out, "  - restart\n") {
				t.Fatalf("expected action to be printed without a success rate, got %q", out)
			}
		})
	}
}

func TestSignaturesCommandRendersValidShape(t *testing.T) {
	body := `{"data":{"signatures":[{"id":"sig_1","pattern":"pay.*fail","count":3,"lastSeen":"2026-01-01"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := config.Config{Cortex: &config.CortexConfig{URL: srv.URL}}
	out, err := captureStdout(t, func() error {
		return SignaturesCommand(context.Background(), cfg, nil)
	})
	if err != nil {
		t.Fatalf("signatures: %v", err)
	}
	if !strings.Contains(out, "Incident Signatures") {
		t.Fatalf("expected section header in output, got %q", out)
	}
	if !strings.Contains(out, "sig_1") {
		t.Fatalf("expected signature id in output, got %q", out)
	}
}

// errReader emits data first, then fails with err on a later read. This
// reproduces a stream truncated mid-read, the condition bufio.Scanner
// reports through Scanner.Err().
type errReader struct {
	data []byte
	err  error
	sent bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	if !r.sent {
		r.sent = true
		return 0, r.err
	}
	return 0, io.EOF
}

func TestStreamLinesReturnsScannerError(t *testing.T) {
	wantErr := errors.New("stream truncated")
	r := &errReader{data: []byte(`{"level":"info","event":"test.event"}` + "\n"), err: wantErr}

	_, gotErr := captureStdout(t, func() error {
		return streamLines(context.Background(), r)
	})
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("streamLines error = %v, want %v", gotErr, wantErr)
	}
}

func TestStreamLinesCleanEOFReturnsNil(t *testing.T) {
	r := strings.NewReader(`{"level":"info","event":"test.event"}` + "\n")
	_, gotErr := captureStdout(t, func() error {
		return streamLines(context.Background(), r)
	})
	if gotErr != nil {
		t.Fatalf("streamLines error = %v, want nil", gotErr)
	}
}

func TestTailCommandPropagatesStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tail" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		// Declaring more bytes than we write makes the connection close
		// early, so the client body read fails with io.ErrUnexpectedEOF.
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"level":"info","event":"test.event"}` + "\n"))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	_, err := captureStdout(t, func() error {
		return TailCommand(context.Background(), cfg, nil)
	})
	if err == nil {
		t.Fatal("TailCommand returned nil for a truncated stream, want an error")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("TailCommand error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestBenchCommandRejectsNonPositiveNumericFlags(t *testing.T) {
	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"negative rate", []string{"--rate", "-1"}, "--rate must be positive"},
		{"zero rate", []string{"--rate", "0"}, "--rate must be positive"},
		{"negative size", []string{"--size", "-1"}, "--size must be positive"},
		{"zero size", []string{"--size", "0"}, "--size must be positive"},
		{"negative batch size", []string{"--batch-size", "-1"}, "--batch-size must be positive"},
		{"zero batch size", []string{"--batch-size", "0"}, "--batch-size must be positive"},
		{"negative duration", []string{"--duration", "-1s"}, "--duration must be positive"},
		{"zero duration", []string{"--duration", "0s"}, "--duration must be positive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := BenchCommand(context.Background(), cfg, tc.args)
			if err == nil {
				t.Fatalf("BenchCommand(%v) succeeded; expected validation error", tc.args)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BenchCommand(%v) error = %q; want it to contain %q", tc.args, err, tc.want)
			}
		})
	}
}

func TestEventStringField(t *testing.T) {
	tests := []struct {
		name     string
		ev       map[string]any
		key      string
		fallback string
		want     string
	}{
		{name: "missing key", ev: map[string]any{}, key: "level", fallback: "unknown", want: "unknown"},
		{name: "explicit null", ev: map[string]any{"level": nil}, key: "level", fallback: "unknown", want: "unknown"},
		{name: "string value", ev: map[string]any{"level": "warn"}, key: "level", fallback: "unknown", want: "warn"},
		{name: "empty string is preserved", ev: map[string]any{"event": ""}, key: "event", fallback: "-", want: ""},
		{name: "non-string value is preserved", ev: map[string]any{"level": float64(3)}, key: "level", fallback: "unknown", want: "3"},
		{name: "missing text field", ev: map[string]any{}, key: "service", fallback: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventStringField(tc.ev, tc.key, tc.fallback); got != tc.want {
				t.Fatalf("eventStringField() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStreamLinesMissingFieldsDoNotRenderNil(t *testing.T) {
	input := strings.Join([]string{
		`{"event":"checkout.completed"}`,
		`{"level":null,"timestamp":null,"service":null,"event":null}`,
		`{"level":"error","timestamp":"2026-01-01T00:00:00Z","service":"checkout","event":"payment.failed"}`,
	}, "\n")

	out, err := captureStdout(t, func() error {
		streamLines(context.Background(), strings.NewReader(input))
		return nil
	})
	if err != nil {
		t.Fatalf("streamLines: %v", err)
	}
	if strings.Contains(out, "<nil>") {
		t.Fatalf("missing fields rendered as <nil>: %s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Fatalf("missing level should render a placeholder, got %s", out)
	}
	if !strings.Contains(out, "checkout.completed") || !strings.Contains(out, "payment.failed") {
		t.Fatalf("expected both events in output, got %s", out)
	}
}

func TestWatchCommandMissingFieldsDoNotRenderNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tail" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"event":"watch.event"}` + "\n"))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	out, err := captureStdout(t, func() error {
		return WatchCommand(context.Background(), cfg, nil)
	})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if strings.Contains(out, "<nil>") {
		t.Fatalf("missing fields rendered as <nil>: %s", out)
	}
	if !strings.Contains(out, "unknown") || !strings.Contains(out, "watch.event") {
		t.Fatalf("expected sanitized event output, got %s", out)
	}
}

func TestParseBlueprintColumnsTrimsComponents(t *testing.T) {
	cols, err := parseBlueprintColumns("name : int : path")
	if err != nil {
		t.Fatalf("parse columns: %v", err)
	}
	col, ok := cols["name"].(map[string]string)
	if !ok {
		t.Fatalf("expected column map, got %T", cols["name"])
	}
	if col["duckdb_type"] != "int" || col["json_path"] != "path" {
		t.Fatalf("expected trimmed components, got %#v", col)
	}
	if _, exists := cols["name "]; exists {
		t.Fatalf("untrimmed column name key present: %#v", cols)
	}
}

func TestParseBlueprintColumnsMultiple(t *testing.T) {
	cols, err := parseBlueprintColumns("id:INTEGER:$.id, user_name:TEXT:$.user.name")
	if err != nil {
		t.Fatalf("parse columns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d: %#v", len(cols), cols)
	}
	col, ok := cols["user_name"].(map[string]string)
	if !ok || col["duckdb_type"] != "TEXT" || col["json_path"] != "$.user.name" {
		t.Fatalf("unexpected user_name column: %#v", cols["user_name"])
	}
}

func TestParseBlueprintColumnsRejectsInvalidColumns(t *testing.T) {
	tests := []struct {
		name    string
		columns string
	}{
		{"empty name", ":int:path"},
		{"empty type", "name::path"},
		{"empty path", "name:int:"},
		{"whitespace name", "   :int:path"},
		{"invalid identifier", "bad-name:int:path"},
		{"too few parts", "name:int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseBlueprintColumns(tt.columns); err == nil {
				t.Fatalf("expected error for %q", tt.columns)
			}
		})
	}
}

func TestBlueprintAddSendsTrimmedColumns(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/schema/blueprint" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := SchemaCommand(cfg, []string{"blueprint", "add", "--name", "bp", "--columns", "name : int : path"}); err != nil {
		t.Fatalf("blueprint add: %v", err)
	}
	if !strings.Contains(gotBody, `"name":{"duckdb_type":"int","json_path":"path"}`) {
		t.Fatalf("expected trimmed column values in payload, got %s", gotBody)
	}
}

func TestBlueprintAddRejectsMalformedColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("collector should not be called for malformed columns")
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := SchemaCommand(cfg, []string{"blueprint", "add", "--name", "bp", "--columns", ":int:path"}); err == nil {
		t.Fatalf("expected error for empty column name")
	}
}

func TestExportHelpDoesNotAdvertiseParquet(t *testing.T) {
	cfg := config.Config{CollectorURL: "http://127.0.0.1:9308"}
	help, err := captureStderr(t, func() error {
		return ExportCommand(cfg, []string{"-h"})
	})
	if err != flag.ErrHelp {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
	if !strings.Contains(help, "ndjson") {
		t.Fatalf("expected ndjson to be advertised in export help, got %s", help)
	}
	if strings.Contains(help, "parquet") {
		t.Fatalf("export help must not advertise unsupported parquet format, got %s", help)
	}
}

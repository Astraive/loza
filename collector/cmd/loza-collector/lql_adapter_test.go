package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

func testLQLBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("LOZA_LQL_BINARY"); binary != "" {
		return binary
	}
	candidates := []string{
		filepath.Join("..", "..", "..", "..", "lql", "target", "debug", "lql.exe"),
		filepath.Join("..", "..", "..", "lql", "target", "debug", "lql.exe"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("LQL binary unavailable")
	return ""
}

func testLQLConfig(binary string) collectorConfig {
	return collectorConfig{
		lqlBinary: binary, lqlExpectedProtocol: 1, lqlExpectedCompiler: "0.5.1",
		lqlExpectedLanguage: "0.1", lqlStartupTimeout: 5e9, lqlCompileTimeout: 5e9,
		lqlMaxConcurrent: 1,
	}
}

func TestLQLStdioCompilerRejectsConcurrentRequests(t *testing.T) {
	_, err := newLQLStdioCompiler(context.Background(), collectorConfig{
		lqlBinary:        "lql",
		lqlMaxConcurrent: 2,
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one concurrent request") {
		t.Fatalf("expected sequential stdio validation error, got %v", err)
	}
}

// TestNoDeadEscapeLQLStringHelper guards issue #481 (MASS-0109): escapeLQLString
// was declared in lql_adapter.go but never referenced anywhere in the collector
// tree. Assert the unused helper stays removed so it cannot silently reappear.
func TestNoDeadEscapeLQLStringHelper(t *testing.T) {
	const dead = "escapeLQLString"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, entry.Name(), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == dead {
				t.Fatalf("%s reintroduces dead helper %s", entry.Name(), dead)
			}
		}
	}
}

func TestLQLStdioCompilerHandshakeAndScope(t *testing.T) {
	cfg := testLQLConfig(testLQLBinary(t))
	compiler, err := newLQLStdioCompiler(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer compiler.Close(context.Background())
	plan, err := compiler.Compile(context.Background(), LQLCompileRequest{
		Source: "from events | where level = \"error\"", Target: "duckdb", Limit: 10,
		Scope: LQLScope{Collector: "acme", Environment: "prod"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SQL == "" || len(plan.Args) == 0 {
		t.Fatalf("expected parameterized plan, got %#v", plan)
	}
	if len(plan.Args) < 3 {
		t.Fatalf("expected query, scope, and limit bindings, got %#v", plan.Args)
	}
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE events (level VARCHAR, collector VARCHAR, environment VARCHAR);
		INSERT INTO events VALUES ('error', 'acme', 'prod'), ('info', 'acme', 'prod'), ('error', 'other', 'prod')`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(plan.SQL, plan.Args...)
	if err != nil {
		t.Fatalf("execute compiled plan: %v\nSQL: %s\nargs: %#v", err, plan.SQL, plan.Args)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one scoped error event, got %d", count)
	}
}

func TestLQLStdioCompilerRestartsAfterProcessLoss(t *testing.T) {
	compiler, err := newLQLStdioCompiler(context.Background(), testLQLConfig(testLQLBinary(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer compiler.Close(context.Background())

	compiler.mu.Lock()
	previousPID := compiler.cmd.Process.Pid
	_ = compiler.killLocked()
	compiler.mu.Unlock()

	plan, err := compiler.Compile(context.Background(), LQLCompileRequest{
		Source: "from events | take 1",
		Target: "duckdb",
		Limit:  1,
	})
	if err != nil {
		t.Fatalf("compile after subprocess loss: %v", err)
	}
	if plan.SQL == "" {
		t.Fatal("restarted compiler returned an empty plan")
	}
	if compiler.cmd == nil || compiler.cmd.Process == nil || compiler.cmd.Process.Pid == previousPID {
		t.Fatalf("compiler process was not replaced: old pid=%d", previousPID)
	}
}

func TestLQLStdioCompilerBacksOffFailedRestarts(t *testing.T) {
	binary := testLQLBinary(t)
	compiler, err := newLQLStdioCompiler(context.Background(), testLQLConfig(binary))
	if err != nil {
		t.Fatal(err)
	}
	defer compiler.Close(context.Background())

	compiler.mu.Lock()
	_ = compiler.killLocked()
	compiler.binary = filepath.Join(t.TempDir(), "missing-lql")
	compiler.mu.Unlock()

	request := LQLCompileRequest{Source: "from events | take 1", Target: "duckdb", Limit: 1}
	if _, err := compiler.Compile(context.Background(), request); err == nil {
		t.Fatal("expected failed compiler restart")
	}

	compiler.mu.Lock()
	compiler.binary = binary
	compiler.mu.Unlock()
	if _, err := compiler.Compile(context.Background(), request); err == nil {
		t.Fatal("expected restart backoff after a failed launch")
	}

	time.Sleep(150 * time.Millisecond)
	if _, err := compiler.Compile(context.Background(), request); err != nil {
		t.Fatalf("compile after restart backoff: %v", err)
	}
}

// discardWriteCloser stands in for the compiler's stdin in requestLocked tests.
type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

// signalingReader reports the first Read call and can be held open with gate.
// It lets a test observe which pipe a reader goroutine actually attached to.
type signalingReader struct {
	called chan struct{}
	gate   chan struct{}
	line   string
	once   sync.Once
}

func (r *signalingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.called) })
	if r.gate != nil {
		<-r.gate
	}
	if r.line == "" {
		return 0, io.EOF
	}
	n := copy(p, r.line)
	r.line = ""
	return n, nil
}

// TestRequestLockedRejectsMismatchedResponseID guards the response-id check:
// a reply belonging to another (for example, stale) request must not satisfy
// the in-flight one.
func TestRequestLockedRejectsMismatchedResponseID(t *testing.T) {
	compiler := &lqlStdioCompiler{
		stdin:    discardWriteCloser{},
		stdout:   bufio.NewReader(strings.NewReader(`{"id":999,"result":{"ok":true}}` + "\n")),
		protocol: 1,
		language: "0.1",
	}
	_, err := compiler.requestLocked(context.Background(), "compile", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "id mismatch") {
		t.Fatalf("expected response id mismatch error, got %v", err)
	}
}

// TestRequestLockedBindsReaderBeforeTimeout reproduces the reported race: a
// request that times out must leave its reader goroutine attached to the
// process it started with, so a restart's new stdout is never consumed by the
// abandoned goroutine. Running on one P keeps the goroutine from being
// scheduled until after the simulated restart swaps c.stdout.
func TestRequestLockedBindsReaderBeforeTimeout(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)

	for attempt := 0; attempt < 8; attempt++ {
		oldSource := &signalingReader{
			called: make(chan struct{}),
			gate:   make(chan struct{}),
			line:   `{"id":1,"result":{}}` + "\n",
		}
		newSource := &signalingReader{
			called: make(chan struct{}),
			line:   `{"id":1,"result":{"other":true}}` + "\n",
		}
		compiler := &lqlStdioCompiler{
			stdin:    discardWriteCloser{},
			stdout:   bufio.NewReader(oldSource),
			protocol: 1,
			language: "0.1",
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // make the request return through ctx.Done() immediately

		if _, err := compiler.requestLocked(ctx, "compile", nil); !errors.Is(err, context.Canceled) {
			close(oldSource.gate)
			t.Fatalf("attempt %d: expected cancellation, got %v", attempt, err)
		}

		// Simulate killLocked/startLocked installing a new process's stdout.
		compiler.stdout = bufio.NewReader(newSource)

		select {
		case <-newSource.called:
			close(oldSource.gate)
			t.Fatalf("attempt %d: timed-out reader goroutine consumed the replacement stdout", attempt)
		case <-oldSource.called:
			close(oldSource.gate)
		case <-time.After(2 * time.Second):
			close(oldSource.gate)
			t.Fatalf("attempt %d: reader goroutine did not start", attempt)
		}
	}
}

// lqlRecordingWriter captures the request frame without requiring the external
// lql binary, so reply correlation can be tested in isolation.
type lqlRecordingWriter struct {
	buf *bytes.Buffer
}

func (w lqlRecordingWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }
func (lqlRecordingWriter) Close() error                  { return nil }

func newFramedTestCompiler(replies []string, nextID int64) (*lqlStdioCompiler, *bytes.Buffer) {
	written := &bytes.Buffer{}
	framed := ""
	if len(replies) > 0 {
		framed = strings.Join(replies, "\n") + "\n"
	}
	compiler := &lqlStdioCompiler{
		stdin:  lqlRecordingWriter{buf: written},
		stdout: bufio.NewReader(strings.NewReader(framed)),
		nextID: nextID,
	}
	return compiler, written
}

// A stale reply (from a previous request or older process) that arrives before
// the matching reply must not be returned as the current response.
func TestLQLRequestLockedCorrelatesReplyByID(t *testing.T) {
	compiler, written := newFramedTestCompiler([]string{
		`{"id":1,"result":{"marker":"stale-from-old-process"}}`,
		`{"id":41,"result":{"marker":"stale-previous-request"}}`,
		`{"id":42,"result":{"marker":"correct"}}`,
	}, 41)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := compiler.requestLocked(ctx, "compile", map[string]any{"query": "from events"})
	if err != nil {
		t.Fatalf("requestLocked: %v", err)
	}
	var body struct {
		Marker string `json:"marker"`
	}
	if err := json.Unmarshal(response.Result, &body); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if body.Marker != "correct" {
		t.Fatalf("got reply marker %q, want %q; reply was not correlated by id", body.Marker, "correct")
	}

	var sent struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(written.Bytes(), &sent); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if sent.ID != 42 {
		t.Fatalf("request id = %d, want 42", sent.ID)
	}
}

// When the only available reply carries a different id, the request must fail
// rather than silently accepting that reply as its own.
func TestLQLRequestLockedRejectsMismatchedReply(t *testing.T) {
	compiler, _ := newFramedTestCompiler([]string{
		`{"id":7,"result":{"marker":"stale"}}`,
	}, 41)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response, err := compiler.requestLocked(ctx, "compile", map[string]any{})
	if err == nil {
		t.Fatalf("expected mismatched-id reply to be rejected, got response %+v", response)
const lqlMockServerEnv = "LOZA_LQL_MOCK_STDIO"

// init turns the test binary into a minimal LQL stdio subprocess when it is
// re-executed with lqlMockServerEnv set. It speaks just enough of the protocol
// for the adapter: a handshake, an immediately answered "compile" request, and
// a deliberately slow "slow" request used to exercise cancellation.
func init() {
	if os.Getenv(lqlMockServerEnv) != "1" || len(os.Args) < 3 || os.Args[1] != "serve" || os.Args[2] != "--stdio" {
		return
	}
	runLQLMockStdioServer()
	os.Exit(0)
}

func runLQLMockStdioServer() {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var req struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				Query string `json:"query"`
			} `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		var result any
		switch req.Method {
		case "handshake":
			result = map[string]any{
				"compiler_version": "0.5.1",
				"language_version": "0.1",
				"protocol_version": 1,
			}
		case "slow":
			time.Sleep(300 * time.Millisecond)
			result = map[string]any{"plan": lqlMockPlan("slow")}
		default:
			result = map[string]any{"plan": lqlMockPlan(req.Params.Query)}
		}
		line, _ := json.Marshal(map[string]any{"id": req.ID, "result": result})
		_, _ = writer.Write(line)
		_ = writer.WriteByte('\n')
		_ = writer.Flush()
	}
}

func lqlMockPlan(query string) map[string]any {
	return map[string]any{
		"sql":           "SELECT '" + query + "'",
		"parameters":    []any{},
		"output_schema": []any{},
	}
}

// TestLQLStdioCompilerCancelThenTwoRequests verifies that cancelling one
// request cannot desync the stdio protocol: the persistent reader drains the
// stale response, and two subsequent requests each get their own response.
func TestLQLStdioCompilerCancelThenTwoRequests(t *testing.T) {
	t.Setenv(lqlMockServerEnv, "1")
	binary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := newLQLStdioCompiler(context.Background(), testLQLConfig(binary))
	if err != nil {
		t.Fatal(err)
	}
	defer compiler.Close(context.Background())

	// A single reader owns stdout for the lifetime of the subprocess.
	if got := compiler.activeReaders.Load(); got != 1 {
		t.Fatalf("expected one stdout reader after handshake, got %d", got)
	}

	// Cancel a request while the mock subprocess is still producing it.
	compiler.mu.Lock()
	slowCtx, cancelSlow := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = compiler.requestLocked(slowCtx, "slow", map[string]any{"query": "cancelled"})
	compiler.mu.Unlock()
	cancelSlow()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancelled request to time out, got %v", err)
	}

	compiler.pendingMu.Lock()
	pendingAfterCancel := len(compiler.pending)
	compiler.pendingMu.Unlock()
	if pendingAfterCancel != 0 {
		t.Fatalf("expected no pending requests after cancel, got %d", pendingAfterCancel)
	}

	// Let the stale response arrive; the reader must drain and discard it, and
	// must remain alive rather than leaking a blocked per-request goroutine.
	time.Sleep(400 * time.Millisecond)
	if got := compiler.activeReaders.Load(); got != 1 {
		t.Fatalf("expected one persistent reader after cancellation, got %d", got)
	}
	compiler.mu.Lock()
	readerDone := compiler.readerDone
	compiler.mu.Unlock()
	select {
	case <-readerDone:
		t.Fatal("stdout reader stopped after a cancelled request")
	default:
	}

	// Two follow-up requests must each receive their own response.
	for _, query := range []string{"alpha", "beta"} {
		compiler.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		resp, err := compiler.requestLocked(ctx, "compile", map[string]any{"query": query})
		compiler.mu.Unlock()
		cancel()
		if err != nil {
			t.Fatalf("request %q after cancellation: %v", query, err)
		}
		var body struct {
			Plan struct {
				SQL string `json:"sql"`
			} `json:"plan"`
		}
		if err := json.Unmarshal(resp.Result, &body); err != nil {
			t.Fatalf("decode response for %q: %v", query, err)
		}
		if want := "SELECT '" + query + "'"; body.Plan.SQL != want {
			t.Fatalf("request %q got SQL %q, want %q (protocol desync)", query, body.Plan.SQL, want)
		}
	}
}

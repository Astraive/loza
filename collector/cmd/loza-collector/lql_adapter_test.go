package main

import (
	"bufio"
	"context"
	"database/sql"
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

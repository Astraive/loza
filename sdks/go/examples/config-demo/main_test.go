package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// recordingSetter records the variables it is asked to set and can be told to
// fail on a specific key.
type recordingSetter struct {
	calls   [][2]string
	failKey string
	err     error
}

func (s *recordingSetter) set(key, value string) error {
	s.calls = append(s.calls, [2]string{key, value})
	if s.failKey != "" && key == s.failKey {
		return s.err
	}
	return nil
}

func TestSetDemoEnvVarsSetsAllVariables(t *testing.T) {
	s := &recordingSetter{}
	if err := setDemoEnvVars(s.set); err != nil {
		t.Fatalf("setDemoEnvVars returned unexpected error: %v", err)
	}
	if len(s.calls) != len(envVars) {
		t.Fatalf("set %d variables, want %d", len(s.calls), len(envVars))
	}
	for i, want := range envVars {
		if s.calls[i] != want {
			t.Errorf("call %d = %v, want %v", i, s.calls[i], want)
		}
	}
}

func TestSetDemoEnvVarsStopsOnError(t *testing.T) {
	boom := errors.New("boom")
	s := &recordingSetter{failKey: envVars[2][0], err: boom}

	err := setDemoEnvVars(s.set)
	if err == nil {
		t.Fatal("setDemoEnvVars returned nil error, want an error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap the original failure %v", err, boom)
	}
	if len(s.calls) != 3 {
		t.Errorf("attempted %d sets after failure, want 3 (stop on first error)", len(s.calls))
	}
	if !strings.Contains(err.Error(), envVars[2][0]) {
		t.Errorf("error %q does not identify the failing key", err.Error())
	}
}

// TestRunEnvExampleReportsSetenvFailure reproduces the reported behavior: a
// failed os.Setenv must be surfaced, and the demo must not print values that
// claim to come from env configuration while defaults are still in effect.
func TestRunEnvExampleReportsSetenvFailure(t *testing.T) {
	failKey := envVars[0][0]
	boom := errors.New("permission denied")
	s := &recordingSetter{failKey: failKey, err: boom}

	var out bytes.Buffer
	err := runEnvExample(&out, s.set)
	if !errors.Is(err, boom) {
		t.Fatalf("runEnvExample error = %v, want wrapped %v", err, boom)
	}

	got := out.String()
	if !strings.Contains(got, "Unable to configure environment") {
		t.Errorf("output does not report the failure:\n%s", got)
	}
	for _, misleading := range []string{"  Service:", "  Version:", "from env"} {
		if strings.Contains(got, misleading) {
			t.Errorf("output still claims env-derived values despite failure (%q):\n%s", misleading, got)
		}
	}
}

func TestRunEnvExampleSuccessPrintsValues(t *testing.T) {
	s := &recordingSetter{}
	var out bytes.Buffer
	if err := runEnvExample(&out, s.set); err != nil {
		t.Fatalf("runEnvExample returned unexpected error: %v", err)
	}
	got := out.String()
	const heading = "2. Configuration via environment variables:"
	if count := strings.Count(got, heading); count != 1 {
		t.Errorf("environment example printed heading %d times, want 1:\n%s", count, got)
	}
	if len(s.calls) != len(envVars) {
		t.Errorf("environment example set %d variables, want %d", len(s.calls), len(envVars))
	}
}

func TestMainRunsEnvExampleOnce(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var mainFn *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil {
		t.Fatal("main function not found in main.go")
	}

	runCalls := 0
	headings := 0
	setterCalls := 0
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			switch fn := node.Fun.(type) {
			case *ast.Ident:
				if fn.Name == "runEnvExample" {
					runCalls++
				}
			case *ast.SelectorExpr:
				if fn.Sel.Name == "Setenv" {
					setterCalls++
				}
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING && strings.Trim(node.Value, "\"") == "2. Configuration via environment variables:" {
				headings++
			}
		}
		return true
	})

	if runCalls != 1 {
		t.Errorf("runEnvExample called %d times in main.go, want 1", runCalls)
	}
	if headings != 0 {
		t.Errorf("inline Example 2 heading appears %d times in main.go, want 0 (heading is printed by runEnvExample)", headings)
	}
	if setterCalls != 0 {
		t.Errorf("inline os.Setenv calls appear %d times in main.go, want 0 (setters are called by runEnvExample)", setterCalls)
	}
}
// TestMainShutsDownLoggerExactlyOnce guards against re-introducing a second
// Logger.Shutdown call in the config demo. Shutdown is not idempotent: it
// flushes and closes every sink on each call, and fileSink.Close closes the
// underlying file again, yielding "file already closed". The demo must call
// Shutdown exactly once (the explicit call prints its result), so deferring a
// second call alongside the explicit one is a correctness bug.
func TestMainShutsDownLoggerExactlyOnce(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Shutdown" {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "logger" {
			return true
		}
		count++
		return true
	})

	if count != 1 {
		t.Fatalf("logger.Shutdown called %d times in main.go; want exactly 1 "+
			"(defer + explicit double-shutdown is a bug)", count)
	}
}

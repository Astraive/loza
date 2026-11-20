package main

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestDeliverAndShutdownSurfacesErrors is the regression test for the defect
// where delivery and shutdown errors were discarded via defer. Both failures
// must now be reported to the caller.
func TestDeliverAndShutdownSurfacesErrors(t *testing.T) {
	emitErr := errors.New("sink delivery failed")
	shutdownErr := errors.New("shutdown failed")

	var calls []string
	emit := func(context.Context) error {
		calls = append(calls, "emit")
		return emitErr
	}
	shutdown := func(context.Context) error {
		calls = append(calls, "shutdown")
		return shutdownErr
	}

	err := deliverAndShutdown(context.Background(), emit, shutdown)
	if err == nil {
		t.Fatal("deliverAndShutdown() returned nil, want both errors surfaced")
	}
	if !errors.Is(err, emitErr) {
		t.Errorf("error does not wrap emit error: %v", err)
	}
	if !errors.Is(err, shutdownErr) {
		t.Errorf("error does not wrap shutdown error: %v", err)
	}
	for _, want := range []string{"emit event", "shutdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not identify %q", err, want)
		}
	}

	// Emit must still run before Shutdown, matching the original defer order.
	if got, want := strings.Join(calls, ","), "emit,shutdown"; got != want {
		t.Errorf("call order = %q, want %q", got, want)
	}
}

func TestDeliverAndShutdownSingleError(t *testing.T) {
	emitErr := errors.New("emit only")

	err := deliverAndShutdown(context.Background(),
		func(context.Context) error { return emitErr },
		func(context.Context) error { return nil },
	)
	if !errors.Is(err, emitErr) {
		t.Errorf("error = %v, want wrapped emit error", err)
	}
}

func TestDeliverAndShutdownNoErrors(t *testing.T) {
	called := false
	err := deliverAndShutdown(context.Background(),
		func(context.Context) error { called = true; return nil },
		func(context.Context) error { return nil },
	)
	if err != nil {
		t.Errorf("deliverAndShutdown() = %v, want nil", err)
	}
	if !called {
		t.Error("emit was not called")
	}
}

// TestMainEmitsBeforeSleep guards against re-introducing the deferred Emit in
// this example. The httpbatch sink only receives an event when Emit runs, and
// Finish merely records the outcome. With `defer loza.Emit(ctx)` the buffer
// stays empty for the whole 500ms sleep (which is meant to exceed the 250ms
// FlushInterval), so no interval flush can occur; delivery is pushed back to
// the deferred Shutdown. The example must call Emit explicitly, before the
// sleep, so the interval ticker can flush the buffered event.
func TestMainEmitsBeforeSleep(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var mainFn *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "main" {
			mainFn = fn
			break
		}
	}
	if mainFn == nil || mainFn.Body == nil {
		t.Fatal("main function not found in main.go")
	}

	isLozaCall := func(expr ast.Expr, name string) bool {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return false
		}
		ident, ok := sel.X.(*ast.Ident)
		return ok && ident.Name == "loza"
	}

	// Record the source range of every deferred call so its nested Emit call is
	// not mistaken for an explicit one.
	var deferRanges []struct{ start, end token.Pos }
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		if d, ok := n.(*ast.DeferStmt); ok {
			deferRanges = append(deferRanges, struct{ start, end token.Pos }{d.Pos(), d.End()})
		}
		return true
	})
	inDefer := func(pos token.Pos) bool {
		for _, r := range deferRanges {
			if pos >= r.start && pos < r.end {
				return true
			}
		}
		return false
	}

	deferredEmit, explicitEmit, sleepPos := 0, 0, token.NoPos
	var emitPos token.Pos
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isLozaCall(call, "Emit") {
			if inDefer(call.Pos()) {
				deferredEmit++
			} else {
				explicitEmit++
				emitPos = call.Pos()
			}
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sleep" {
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
				sleepPos = call.Pos()
			}
		}
		return true
	})

	if deferredEmit != 0 {
		t.Fatalf("loza.Emit is deferred %d time(s) in main; it must run before the sleep "+
			"so the httpbatch sink can flush the event during the wait", deferredEmit)
	}
	if explicitEmit != 1 {
		t.Fatalf("loza.Emit called explicitly %d time(s) in main; want exactly 1", explicitEmit)
	}
	if sleepPos == token.NoPos {
		t.Fatal("time.Sleep call not found in main; nothing to flush the batch before")
	}
	if emitPos >= sleepPos {
		t.Fatal("loza.Emit must be called before time.Sleep in main")
	}
}

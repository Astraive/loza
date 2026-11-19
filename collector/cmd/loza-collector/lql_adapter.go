package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type lqlStdioCompiler struct {
	mu               sync.Mutex
	cmd              *exec.Cmd
	stdin            io.WriteCloser
	stdout           *bufio.Reader
	binary           string
	protocol         int
	compiler         string
	language         string
	timeout          time.Duration
	startupTimeout   time.Duration
	sem              chan struct{}
	nextID           int64
	restartFailures  uint
	restartNotBefore time.Time
	lastRestartError error

	// A single reader goroutine owns the stdout pipe for the lifetime of the
	// subprocess. It dispatches each response to the request waiting on it, so
	// a cancelled request can never leave unread bytes that desync the stream.
	pendingMu sync.Mutex
	pending   map[int64]chan lqlRPCResult
	// outstanding records request IDs in issue order, including cancelled
	// ones, so a protocol variant that omits the response id can still match
	// responses in order and discard the reply to a cancelled request.
	outstanding   []int64
	readerDone    chan struct{}
	activeReaders atomic.Int64
}

func newLQLStdioCompiler(ctx context.Context, cfg collectorConfig) (*lqlStdioCompiler, error) {
	if strings.TrimSpace(cfg.lqlBinary) == "" {
		return nil, errors.New("lql compiler binary is empty")
	}
	if cfg.lqlMaxConcurrent > 1 {
		return nil, errors.New("lql stdio compiler supports exactly one concurrent request")
	}
	startup := cfg.lqlStartupTimeout
	if startup <= 0 {
		startup = 5 * time.Second
	}
	compileTimeout := cfg.lqlCompileTimeout
	if compileTimeout <= 0 {
		compileTimeout = 5 * time.Second
	}
	maxConcurrent := 1
	compiler := &lqlStdioCompiler{
		binary: cfg.lqlBinary, protocol: cfg.lqlExpectedProtocol,
		compiler: cfg.lqlExpectedCompiler, language: cfg.lqlExpectedLanguage,
		timeout: compileTimeout, startupTimeout: startup, sem: make(chan struct{}, maxConcurrent),
	}
	if compiler.protocol == 0 {
		compiler.protocol = 1
	}
	if compiler.language == "" {
		compiler.language = "0.1"
	}
	startCtx, cancel := context.WithTimeout(ctx, startup)
	defer cancel()
	if err := compiler.start(startCtx); err != nil {
		return nil, err
	}
	return compiler, nil
}

func (c *lqlStdioCompiler) start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startLocked(ctx)
}

func (c *lqlStdioCompiler) startLocked(ctx context.Context) error {
	cmd := exec.Command(c.binary, "serve", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}
	c.cmd, c.stdin, c.stdout = cmd, stdin, bufio.NewReader(stdout)
	c.startReaderLocked()
	response, err := c.requestLocked(ctx, "handshake", map[string]any{})
	if err != nil {
		_ = c.killLocked()
		return fmt.Errorf("lql handshake failed: %w", err)
	}
	var result struct {
		CompilerVersion string `json:"compiler_version"`
		LanguageVersion string `json:"language_version"`
		ProtocolVersion int    `json:"protocol_version"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		_ = c.killLocked()
		return fmt.Errorf("invalid lql handshake: %w", err)
	}
	if result.ProtocolVersion != c.protocol || (c.compiler != "" && result.CompilerVersion != c.compiler) || result.LanguageVersion != c.language {
		_ = c.killLocked()
		return fmt.Errorf("lql version mismatch: protocol=%d compiler=%s language=%s", result.ProtocolVersion, result.CompilerVersion, result.LanguageVersion)
	}
	c.restartFailures = 0
	c.restartNotBefore = time.Time{}
	c.lastRestartError = nil
	return nil
}

func (c *lqlStdioCompiler) Compile(ctx context.Context, req LQLCompileRequest) (ParameterizedPlan, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return ParameterizedPlan{}, ctx.Err()
	}
	if req.Limit <= 0 {
		req.Limit = 1000
	}
	if req.Limit > 1000 {
		req.Limit = 1000
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return ParameterizedPlan{}, NewLQLCompileError(LQLDiagnostic{Code: "LQL000", Severity: "error", Message: "query is empty"})
	}
	deadline := c.timeout
	if deadline <= 0 {
		deadline = 5 * time.Second
	}
	compileCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureStartedLocked(compileCtx); err != nil {
		return ParameterizedPlan{}, NewLQLCompilerUnavailable(err)
	}
	response, err := c.requestLocked(compileCtx, "compile", map[string]any{
		"query":      source,
		"target":     req.Target,
		"parameters": req.Parameters,
		"schema":     nil,
		"policy": map[string]any{
			"allowed_sources": []string{"events"},
			"max_result_rows": req.Limit,
		},
		"scope": req.Scope,
	})
	if err != nil {
		_ = c.killLocked()
		c.restartFailures = 0
		c.restartNotBefore = time.Time{}
		c.lastRestartError = err
		return ParameterizedPlan{}, NewLQLCompilerUnavailable(err)
	}
	if response.Error != nil {
		if len(response.Error.Diagnostics) > 0 {
			return ParameterizedPlan{}, &LQLCompileError{Diagnostics: response.Error.Diagnostics}
		}
		return ParameterizedPlan{}, NewLQLCompilerUnavailable(errors.New(response.Error.Message))
	}
	var body struct {
		Plan struct {
			SQL        string `json:"sql"`
			Parameters []struct {
				LogicalType string `json:"logical_type"`
				Value       any    `json:"value"`
			} `json:"parameters"`
			OutputSchema []LQLColumn `json:"output_schema"`
		} `json:"plan"`
	}
	if err := json.Unmarshal(response.Result, &body); err != nil {
		_ = c.killLocked()
		c.restartFailures = 0
		c.restartNotBefore = time.Time{}
		c.lastRestartError = err
		return ParameterizedPlan{}, NewLQLCompilerUnavailable(err)
	}
	for index := range body.Plan.OutputSchema {
		if body.Plan.OutputSchema[index].Type == "" {
			body.Plan.OutputSchema[index].Type = body.Plan.OutputSchema[index].FieldType
		}
	}
	args := make([]any, 0, len(body.Plan.Parameters))
	for _, parameter := range body.Plan.Parameters {
		args = append(args, parameter.Value)
	}
	return ParameterizedPlan{SQL: body.Plan.SQL, Args: args, OutputSchema: body.Plan.OutputSchema, Limit: req.Limit}, nil
}

func (c *lqlStdioCompiler) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.killLocked()
}

func (c *lqlStdioCompiler) killLocked() error {
	stdin, cmd := c.stdin, c.cmd
	c.cmd, c.stdin, c.stdout = nil, nil, nil
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd == nil {
		return nil
	}
	_ = cmd.Process.Kill()
	return cmd.Wait()
}

func (c *lqlStdioCompiler) ensureStartedLocked(ctx context.Context) error {
	if c.cmd != nil && c.stdin != nil && c.stdout != nil {
		return nil
	}
	if wait := time.Until(c.restartNotBefore); wait > 0 {
		return fmt.Errorf("lql compiler restart backoff active: %w", c.lastRestartError)
	}
	startCtx := ctx
	cancel := func() {}
	if c.startupTimeout > 0 {
		startCtx, cancel = context.WithTimeout(ctx, c.startupTimeout)
	}
	defer cancel()
	if err := c.startLocked(startCtx); err != nil {
		c.restartFailures++
		c.lastRestartError = err
		c.restartNotBefore = time.Now().Add(lqlRestartBackoff(c.restartFailures))
		return err
	}
	return nil
}

func lqlRestartBackoff(failures uint) time.Duration {
	delay := 100 * time.Millisecond
	for attempt := uint(1); attempt < failures && delay < 5*time.Second; attempt++ {
		delay *= 2
	}
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

type lqlRPCResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code        string          `json:"code"`
		Message     string          `json:"message"`
		Diagnostics []LQLDiagnostic `json:"diagnostics"`
	} `json:"error"`
}

type lqlRPCResult struct {
	response lqlRPCResponse
	err      error
}

// startReaderLocked launches the single goroutine that reads the subprocess
// stdout for the lifetime of the current process. Callers must hold c.mu.
func (c *lqlStdioCompiler) startReaderLocked() {
	done := make(chan struct{})
	c.readerDone = done
	c.pendingMu.Lock()
	c.outstanding = nil
	c.pendingMu.Unlock()
	reader := c.stdout
	go c.readLoop(reader, done)
}

// readLoop continuously drains stdout and routes each response to the request
// waiting for it. It never stops on cancellation: a response for a request
// that has already returned simply has no pending channel and is discarded.
func (c *lqlStdioCompiler) readLoop(reader *bufio.Reader, done chan struct{}) {
	c.activeReaders.Add(1)
	defer c.activeReaders.Add(-1)
	defer close(done)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// readerDone unblocks every waiter for this subprocess generation.
			return
		}
		var response lqlRPCResponse
		if err := json.Unmarshal(line, &response); err != nil {
			// Keep draining: a malformed line must not strand the stream.
			continue
		}
		c.deliver(response)
	}
}

// deliver hands a response to the matching pending request. When the protocol
// variant omits the response id, responses are matched to request IDs in issue
// order; a cancelled request has no pending channel, so its late response is
// discarded instead of being misrouted to a later request.
func (c *lqlStdioCompiler) deliver(response lqlRPCResponse) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if response.ID != 0 {
		c.removeOutstanding(response.ID)
		if ch, ok := c.pending[response.ID]; ok {
			delete(c.pending, response.ID)
			ch <- lqlRPCResult{response: response}
		}
		return
	}
	if len(c.outstanding) == 0 {
		return
	}
	id := c.outstanding[0]
	c.outstanding = c.outstanding[1:]
	if ch, ok := c.pending[id]; ok {
		delete(c.pending, id)
		ch <- lqlRPCResult{response: response}
	}
}

// removeOutstanding drops an id from the issue-order queue. Called with
// pendingMu held.
func (c *lqlStdioCompiler) removeOutstanding(id int64) {
	for index, queued := range c.outstanding {
		if queued == id {
			c.outstanding = append(c.outstanding[:index], c.outstanding[index+1:]...)
			return
		}
	}
}

func (c *lqlStdioCompiler) requestLocked(ctx context.Context, method string, params any) (lqlRPCResponse, error) {
	c.nextID++
	id := c.nextID
	request := map[string]any{"id": id, "method": method, "protocol_version": c.protocol, "language_version": c.language, "params": params}
	line, err := json.Marshal(request)
	if err != nil {
		return lqlRPCResponse{}, err
	}
	// Capture the reader for this request so a goroutine left over from a
	// timed-out request cannot consume replies from a restarted process.
	stdout := c.stdout
	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		return lqlRPCResponse{}, err
	}
	type readResult struct {
		response lqlRPCResponse
		err      error
	}
	result := make(chan readResult, 1)
	go func() {
		var mismatchID int64
		sawMismatch := false
		for {
			responseLine, readErr := stdout.ReadBytes('\n')
			if readErr != nil {
				// If every candidate reply was drained as a mismatch, surface
				// the id mismatch so callers can tell a stale reply from a
				// genuine read failure instead of only seeing the EOF/error.
				if sawMismatch {
					result <- readResult{err: fmt.Errorf("lql response id mismatch: got %d, want %d: %w", mismatchID, id, readErr)}
				} else {
					result <- readResult{err: readErr}
				}
				return
			}
			var response lqlRPCResponse
			if err := json.Unmarshal(responseLine, &response); err != nil {
				result <- readResult{err: err}
				return
			}
			// A compiler that omits the id leaves it at zero; accept those
			// replies for legacy compatibility. Drain stale, duplicated, or
			// out-of-order replies instead of accepting the first line as the
			// answer to this request. This also discards any reply left in
			// flight across a restart, so only a reply correlated to this
			// request (or one with no id at all) can satisfy it.
			if response.ID != 0 && response.ID != id {
				sawMismatch = true
				mismatchID = response.ID
				continue
			}
			result <- readResult{response: response}
			return
		}
	}()
	select {
	case <-ctx.Done():
		return lqlRPCResponse{}, ctx.Err()
	case read := <-result:
		if read.err != nil {
			return lqlRPCResponse{}, read.err
		}
		return read.response, nil
	}
}

	resultCh := make(chan lqlRPCResult, 1)
	readerDone := c.readerDone
	c.pendingMu.Lock()
	if c.pending == nil {
		c.pending = make(map[int64]chan lqlRPCResult)
	}
	c.pending[id] = resultCh
	c.outstanding = append(c.outstanding, id)
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if _, err := c.stdin.Write(append(line, '\n')); err != nil {
		return lqlRPCResponse{}, err
	}
	select {
	case <-ctx.Done():
		return lqlRPCResponse{}, ctx.Err()
	case <-readerDone:
		return lqlRPCResponse{}, errors.New("lql stdio reader stopped")
	case res := <-resultCh:
		return res.response, res.err
	}
}

func escapeLQLString(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

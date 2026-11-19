package collectorsync

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
)

// startManagedProcess starts name with args as a child process whose lifetime is
// bound to a private context created here. The returned stop function cancels
// that context (terminating the child) and then reaps it with Wait.
//
// Binding the child to the context via exec.CommandContext guarantees that
// cancel() has a real effect on the process, instead of discarding the context
// and relying on an ad-hoc Process.Kill. configure, when non-nil, is applied to
// the command before it is started (e.g. to set Dir, Stdout, or Stderr).
func startManagedProcess(name string, args []string, configure func(*exec.Cmd)) (*exec.Cmd, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, name, args...)
	if configure != nil {
		configure(cmd)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, func() {}, err
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			_ = cmd.Wait()
		})
	}
	return cmd, stop, nil
}

// TestManagedProcessStopTerminatesChild is a regression test for the discarded
// context in the live collector integration test. It verifies that stopping a
// managed process actually terminates the child through context cancellation;
// under the old "discarded context + ignored cancel" pattern the child would
// keep running until its natural exit and this test would time out.
func TestManagedProcessStopTerminatesChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep is not available on Windows")
	}

	cmd, stop, err := startManagedProcess("sleep", []string{"60"}, nil)
	if err != nil {
		t.Fatalf("start managed process: %v", err)
	}

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatal("stop() did not terminate the child process via context cancellation")
	}
}

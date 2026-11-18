package commands

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startLongRunningCollector starts a stand-in for the collector server: a
// single process that never exits on its own and ignores SIGTERM/INT, so only
// an explicit Kill (before Wait) can make Wait return.
func startLongRunningCollector(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", "trap '' TERM INT; exec sleep 600")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start long-running collector: %v", err)
	}
	return cmd
}

// TestSuperviseDevStopsCollectorAfterCortexExits reproduces MASS-0025: after
// cortex exits, a long-running collector used to be left blocked in Wait with
// an unreachable kill defer, so the command hung and leaked the process.
func TestSuperviseDevStopsCollectorAfterCortexExits(t *testing.T) {
	collector := startLongRunningCollector(t)
	cortex := exec.Command("sh", "-c", "exit 0")

	done := make(chan error, 1)
	go func() { done <- superviseDev(collector, cortex) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("superviseDev returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("superviseDev hung: collector was not killed after cortex exited")
	}

	if collector.ProcessState == nil {
		t.Fatal("collector was not reaped by Cmd.Wait")
	}
}

// TestSuperviseDevStopsCollectorWhenCortexFails verifies the error path also
// tears down and reaps the collector exactly once.
func TestSuperviseDevStopsCollectorWhenCortexFails(t *testing.T) {
	collector := startLongRunningCollector(t)
	cortex := exec.Command("sh", "-c", "echo cortex failed >&2; exit 7")

	done := make(chan error, 1)
	go func() { done <- superviseDev(collector, cortex) }()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "run cortex") {
			t.Fatalf("expected cortex error, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("superviseDev hung on the cortex error path")
	}

	if collector.ProcessState == nil {
		t.Fatal("collector was not reaped by Cmd.Wait on the cortex error path")
	}
}

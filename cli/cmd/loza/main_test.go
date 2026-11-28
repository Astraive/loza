package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/version"
)

func TestCLIEntryPointsUseLinkedVersion(t *testing.T) {
	const marker = "0.4.2-test"
	originalVersion := version.Version
	version.Version = marker
	t.Cleanup(func() { version.Version = originalVersion })

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "usage", want: "LOZA CLI v" + marker},
		{name: "help", args: []string{"--help"}, want: "LOZA CLI v" + marker},
		{name: "version", args: []string{"--version"}, want: "loza version " + marker},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := captureRunOutput(t, test.args)
			if !strings.Contains(output, test.want) {
				t.Fatalf("output %q does not contain linked version %q", output, test.want)
			}
		})
	}
}

func captureRunOutput(t *testing.T, args []string) string {
	t.Helper()

	file, err := os.CreateTemp(t.TempDir(), "loza-cli-output-")
	if err != nil {
		t.Fatalf("create output capture: %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = file
	t.Cleanup(func() {
		os.Stdout = originalStdout
		_ = file.Close()
	})

	if err := run(args); err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	os.Stdout = originalStdout
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind output capture: %v", err)
	}
	output, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read output capture: %v", err)
	}
	return string(output)
}

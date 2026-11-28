package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConnectGoSDKDocumentationCompiles(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	moduleDir := filepath.Dir(testFile)
	repoRoot := filepath.Clean(filepath.Join(moduleDir, "..", "..", "..", ".."))
	docPath := filepath.Join(repoRoot, "collector", "docs", "connect-go-sdk.md")
	markdown, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read connection guide: %v", err)
	}
	document := strings.ReplaceAll(string(markdown), "\r\n", "\n")
	const openingFence = "```go\n"
	start := strings.Index(document, openingFence)
	if start < 0 {
		t.Fatal("connection guide has no Go code block")
	}
	start += len(openingFence)
	end := strings.Index(document[start:], "\n```")
	if end < 0 {
		t.Fatal("connection guide Go code block is not closed")
	}
	snippet := document[start : start+end]

	source := "package main\n\n" +
		"import (\n" +
		"\tloza \"github.com/astraive/loza/sdks/go\"\n" +
		"\t\"github.com/astraive/loza/sdks/go/src/sinks/httpbatch\"\n" +
		")\n\n" +
		"func compileDocumentedExample() error {\n" +
		snippet + "\n\treturn nil\n}\n\n" +
		"func main() {}\n"
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "connect_go_sdk_doc.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("write compile fixture: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(tempDir, "doc-example.exe"), sourcePath)
	cmd.Dir = moduleDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile documented Go example: %v\n%s", err, output)
	}
}

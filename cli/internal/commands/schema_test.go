package commands

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
)

// TestSchemaMutationDispatch verifies that the "diff" and "publish" subcommands
// dispatch through their explicit operation functions to the correct collector
// endpoint with the constructed payload. Previously the operation was selected
// by a string path whose switch had an unreachable default branch.
func TestSchemaMutationDispatch(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPath    string
		wantVersion string
	}{
		{
			name:        "diff",
			args:        []string{"diff", "--schema-version", "v1.2.3"},
			wantPath:    "/schema/diff",
			wantVersion: "v1.2.3",
		},
		{
			name:        "publish",
			args:        []string{"publish", "--schema-version", "v2.0.0"},
			wantPath:    "/schema/publish",
			wantVersion: "v2.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotMethod, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotMethod = r.Method
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()

			cfg := config.Config{CollectorURL: srv.URL}
			if err := SchemaCommand(cfg, tt.args); err != nil {
				t.Fatalf("schema %s: %v", tt.name, err)
			}

			if gotPath != tt.wantPath {
				t.Fatalf("expected path %s, got %s", tt.wantPath, gotPath)
			}
			if gotMethod != http.MethodPost {
				t.Fatalf("expected method %s, got %s", http.MethodPost, gotMethod)
			}
			if !strings.Contains(gotBody, `"schema_version":"`+tt.wantVersion+`"`) {
				t.Fatalf("expected payload with schema_version %s, got %s", tt.wantVersion, gotBody)
			}
		})
	}
}

// TestRunSchemaMutationDoesNotSwallowOperationError verifies that an error
// returned by the selected operation is propagated to the caller.
func TestRunSchemaMutationDoesNotSwallowOperationError(t *testing.T) {
	wantErr := context.DeadlineExceeded
	err := runSchemaMutation(context.Background(), config.Config{}, func(context.Context, string, []byte) ([]byte, error) {
		return nil, wantErr
	}, nil)
	if err != wantErr {
		t.Fatalf("expected operation error %v, got %v", wantErr, err)
	}
}

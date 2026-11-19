package commands

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/astraive/loza/cli/internal/config"
)

func TestAuditPIIRejectsInvalidLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit string
	}{
		{name: "negative", limit: "-1"},
		{name: "zero", limit: "0"},
		{name: "above max", limit: "5001"},
		{name: "absurdly large", limit: "2147483647"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hit := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hit = true
				_, _ = w.Write([]byte(`{"count":0,"findings":[]}`))
			}))
			defer srv.Close()

			cfg := config.Config{CollectorURL: srv.URL}
			err := AuditCommand(context.Background(), cfg, []string{"pii", "--limit", tt.limit})
			if err == nil {
				t.Fatalf("expected error for --limit %s, got nil", tt.limit)
			}
			if !strings.Contains(err.Error(), "invalid --limit") {
				t.Fatalf("expected invalid limit error, got %v", err)
			}
			if hit {
				t.Fatalf("collector was called with an invalid --limit %s", tt.limit)
			}
		})
	}
}

func TestAuditPIIAcceptsValidLimit(t *testing.T) {
	var got map[string]int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&got); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"count":0,"findings":[]}`))
	}))
	defer srv.Close()

	cfg := config.Config{CollectorURL: srv.URL}
	if err := AuditCommand(context.Background(), cfg, []string{"pii", "--limit", "1000"}); err != nil {
		t.Fatalf("audit pii with valid limit: %v", err)
	}
	if got["limit"] != 1000 {
		t.Fatalf("expected limit 1000 in payload, got %v", got)
	}
}

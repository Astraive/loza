package client

import (
	"context"
	"testing"
)

// TestCortexGraphEscapesIdentifiers verifies that cortex graph lookups route
// their user-supplied identifiers through the shared URL helper just like the
// collector client, escaping "/" , "?", "#", "&", and spaces in the path while
// encoding the depth query parameter with url.Values.
func TestCortexGraphEscapesIdentifiers(t *testing.T) {
	const id = "a/b ?#&c"
	const escaped = "a%2Fb%20%3F%23&c"

	tests := []struct {
		name     string
		wantPath string
		call     func(string) error
	}{
		{
			"service",
			"/graph/service/" + escaped,
			func(base string) error {
				_, err := FetchCortexServiceGraph(context.Background(), base, id, 3)
				return err
			},
		},
		{
			"incident",
			"/graph/incident/" + escaped,
			func(base string) error {
				_, err := FetchCortexIncidentGraph(context.Background(), base, id, 3)
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured capturedRequest
			srv := newCaptureServer(t, &captured)
			defer srv.Close()

			if err := tc.call(srv.URL); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if captured.escapedPath != tc.wantPath {
				t.Errorf("escaped path = %q, want %q", captured.escapedPath, tc.wantPath)
			}
			if captured.rawQuery != "depth=3" {
				t.Errorf("raw query = %q, want depth=3", captured.rawQuery)
			}
		})
	}
}

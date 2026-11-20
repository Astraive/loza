package cortex

import (
	"math"
	"testing"
)

func TestValidateIncidentContextConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		wantErr    bool
	}{
		{name: "zero", confidence: 0.0, wantErr: false},
		{name: "one", confidence: 1.0, wantErr: false},
		{name: "midpoint", confidence: 0.5, wantErr: false},
		{name: "negative", confidence: -0.1, wantErr: true},
		{name: "above one", confidence: 1.1, wantErr: true},
		{name: "positive infinity", confidence: math.Inf(1), wantErr: true},
		{name: "negative infinity", confidence: math.Inf(-1), wantErr: true},
		{name: "NaN", confidence: math.NaN(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &IncidentContext{
				IncidentID: "inc/1",
				Timestamp:  "2026-01-01T00:00:00Z",
				Confidence: tt.confidence,
			}
			err := ValidateIncidentContext(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateIncidentContext() error = %v, wantErr %v (confidence %v)", err, tt.wantErr, tt.confidence)
			}
		})
	}
}

func TestValidateGraphViewNodeIDValue(t *testing.T) {
	tests := []struct {
		name    string
		id      any
		wantErr bool
	}{
		{name: "valid string id", id: "svc-a"},
		{name: "missing id", wantErr: true},
		{name: "nil id", id: nil, wantErr: true},
		{name: "empty id", id: "", wantErr: true},
		{name: "whitespace-only id", id: "   ", wantErr: true},
		{name: "non-string id", id: 42, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := map[string]any{}
			if tc.name != "missing id" {
				node["id"] = tc.id
			}
			gv := &GraphView{Nodes: []map[string]any{node}}
			err := ValidateGraphView(gv)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateGraphView() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateGraphViewEdgeEndpointValue(t *testing.T) {
	tests := []struct {
		name    string
		edge    map[string]any
		wantErr bool
	}{
		{name: "valid source and target", edge: map[string]any{"source": "a", "target": "b"}},
		{name: "valid alternate keys", edge: map[string]any{"from_node_id": "a", "to_node_id": "b"}},
		{name: "nil source and target", edge: map[string]any{"source": nil, "target": nil}, wantErr: true},
		{name: "empty source", edge: map[string]any{"source": "", "target": "b"}, wantErr: true},
		{name: "whitespace-only target", edge: map[string]any{"source": "a", "target": "  "}, wantErr: true},
		{name: "non-string source", edge: map[string]any{"source": 7, "target": "b"}, wantErr: true},
		{name: "missing target", edge: map[string]any{"source": "a"}, wantErr: true},
		{
			name: "nil source falls back to valid alternate",
			edge: map[string]any{"source": nil, "from_node_id": "a", "target": "b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gv := &GraphView{Edges: []map[string]any{tc.edge}}
			err := ValidateGraphView(gv)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateGraphView() error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

package cluster

import (
	"fmt"
	"testing"
)

func TestNewPartitionerClampsNodeIDIntoRange(t *testing.T) {
	tests := []struct {
		name          string
		nodeCount     int
		nodeID        int
		wantNodeCount int
		wantNodeID    int
	}{
		{name: "valid topology unchanged", nodeCount: 3, nodeID: 2, wantNodeCount: 3, wantNodeID: 2},
		{name: "negative nodeID clamped to zero", nodeCount: 3, nodeID: -1, wantNodeCount: 3, wantNodeID: 0},
		{name: "out-of-range high nodeID clamped to last slot", nodeCount: 3, nodeID: 5, wantNodeCount: 3, wantNodeID: 2},
		{name: "single node at id zero unchanged", nodeCount: 1, nodeID: 0, wantNodeCount: 1, wantNodeID: 0},
		{name: "single node with out-of-range id clamped", nodeCount: 1, nodeID: 1, wantNodeCount: 1, wantNodeID: 0},
		{name: "invalid nodeCount normalized and nodeID clamped", nodeCount: 0, nodeID: 5, wantNodeCount: 1, wantNodeID: 0},
		{name: "negative nodeCount normalized and nodeID clamped", nodeCount: -2, nodeID: 7, wantNodeCount: 1, wantNodeID: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPartitioner(tt.nodeCount, tt.nodeID)
			if p.NodeCount() != tt.wantNodeCount {
				t.Errorf("NodeCount() = %d, want %d", p.NodeCount(), tt.wantNodeCount)
			}
			if p.NodeID() != tt.wantNodeID {
				t.Errorf("NodeID() = %d, want %d", p.NodeID(), tt.wantNodeID)
			}
			if p.NodeID() < 0 || p.NodeID() >= p.NodeCount() {
				t.Errorf("nodeID %d outside [0, %d)", p.NodeID(), p.NodeCount())
			}
		})
	}
}

func TestOutOfRangeNodeIDIsNotPermanentlyIdle(t *testing.T) {
	// Regression for the reported defect: with an out-of-range nodeID,
	// ShouldProcess could never return true because PartitionKey is bounded
	// by [0, nodeCount-1]. After the fix the node is assigned a usable slot.
	p := NewPartitioner(3, 5)
	if p.NodeID() != 2 {
		t.Fatalf("NodeID() = %d, want 2", p.NodeID())
	}

	processed := 0
	for i := 0; i < 100; i++ {
		service := fmt.Sprintf("service-%d", i)
		if p.ShouldProcess(service) {
			if got := p.PartitionKey(service); got != p.NodeID() {
				t.Errorf("ShouldProcess(%q) true but PartitionKey() = %d, want %d", service, got, p.NodeID())
			}
			processed++
		}
	}
	if processed == 0 {
		t.Fatal("node with out-of-range nodeID still processes no service")
	}

	// PartitionKey output must remain within the configured range.
	for i := 0; i < 100; i++ {
		key := p.PartitionKey(fmt.Sprintf("service-%d", i))
		if key < 0 || key >= p.NodeCount() {
			t.Fatalf("PartitionKey() = %d outside [0, %d)", key, p.NodeCount())
		}
	}
}

func TestShouldProcessMatchesPartitionKeyForEveryNode(t *testing.T) {
	const nodeCount = 5
	for nodeID := 0; nodeID < nodeCount; nodeID++ {
		p := NewPartitioner(nodeCount, nodeID)
		for i := 0; i < 50; i++ {
			service := fmt.Sprintf("svc-%d", i)
			want := p.PartitionKey(service) == nodeID
			if got := p.ShouldProcess(service); got != want {
				t.Fatalf("node %d: ShouldProcess(%q) = %v, want %v", nodeID, service, got, want)
			}
		}
	}
}

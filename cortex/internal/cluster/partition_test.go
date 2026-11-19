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
func services(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("service-%d", i)
	}
	return out
}

// TestAllNodesAgreeOnMapping verifies that independent Partitioners built for
// the same topology (different nodeID) compute identical ownership and that
// exactly one node processes each service.
func TestAllNodesAgreeOnMapping(t *testing.T) {
	const nodeCount = 7
	nodes := make([]*Partitioner, nodeCount)
	for id := 0; id < nodeCount; id++ {
		nodes[id] = NewPartitioner(nodeCount, id)
	}

	for _, service := range services(500) {
		owner := nodes[0].PartitionKey(service)
		if owner < 0 || owner >= nodeCount {
			t.Fatalf("PartitionKey(%q) = %d, out of range [0,%d)", service, owner, nodeCount)
		}
		processCount := 0
		for id, node := range nodes {
			if got := node.PartitionKey(service); got != owner {
				t.Fatalf("node %d mapped %q to %d, want %d (all nodes must agree)", id, service, got, owner)
			}
			if node.ShouldProcess(service) {
				processCount++
			}
		}
		if processCount != 1 {
			t.Fatalf("%d nodes process %q, want exactly 1 (node %d)", processCount, service, owner)
		}
		if !nodes[owner].ShouldProcess(service) {
			t.Fatalf("owner node %d does not process %q", owner, service)
		}
	}
}

// TestConsistentHashingOnResize is the regression test for the reported
// defect. With plain modulo hashing, changing nodeCount from N to N+1 moves
// roughly N/(N+1) of all services. A real consistent-hash ring moves only
// ~1/(N+1) (the share taken by the new node).
func TestConsistentHashingOnResize(t *testing.T) {
	const (
		oldNodes = 10
		newNodes = 11
		keys     = 3000
	)

	oldRing := NewPartitioner(oldNodes, 0)
	newRing := NewPartitioner(newNodes, 0)

	remapped := 0
	for _, service := range services(keys) {
		if oldRing.PartitionKey(service) != newRing.PartitionKey(service) {
			remapped++
		}
	}
	fraction := float64(remapped) / float64(keys)

	// Modulo hashing remaps ~10/11 (~0.91) of services; consistent hashing
	// remaps ~1/11 (~0.09). A generous bound keeps the test stable while
	// still catching a regression to modulo hashing.
	const maxFraction = 0.25
	if fraction > maxFraction {
		t.Fatalf("growing cluster from %d to %d nodes remapped %.1f%% of services, want <= %.0f%%: "+
			"implementation is not stable consistent hashing", oldNodes, newNodes, fraction*100, maxFraction*100)
	}
}

// TestPartitionKeyDeterministic ensures the ring is derived deterministically
// from nodeCount and does not depend on this node's own ID.
func TestPartitionKeyDeterministic(t *testing.T) {
	for _, nodeCount := range []int{2, 3, 8, 16} {
		a := NewPartitioner(nodeCount, 0)
		b := NewPartitioner(nodeCount, nodeCount-1)
		for _, service := range services(200) {
			if got, want := a.PartitionKey(service), b.PartitionKey(service); got != want {
				t.Fatalf("nodeCount=%d: PartitionKey(%q) depends on nodeID: %d vs %d", nodeCount, service, got, want)
			}
		}
	}
}

func TestClampingAndAccessors(t *testing.T) {
	p := NewPartitioner(0, -5)
	if p.NodeCount() != 1 {
		t.Fatalf("NodeCount() = %d, want 1 (nodeCount clamped)", p.NodeCount())
	}
	if p.NodeID() != 0 {
		t.Fatalf("NodeID() = %d, want 0 (nodeID clamped)", p.NodeID())
	}
	if got := p.PartitionKey("anything"); got != 0 {
		t.Fatalf("PartitionKey with single node = %d, want 0", got)
	}
}

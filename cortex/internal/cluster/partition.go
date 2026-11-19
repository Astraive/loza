package cluster

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// virtualNodesPerNode is the number of ring positions (virtual nodes) assigned
// to each physical node. More virtual nodes give a more even key distribution
// at the cost of a larger ring.
const virtualNodesPerNode = 128

// ringEntry places a physical node at a position on the consistent-hash ring.
type ringEntry struct {
	position uint64
	node     int
}

// Partitioner assigns services to nodes using consistent hashing.
//
// Each physical node owns a fixed set of virtual nodes on a hash ring, so a
// service maps to the first virtual node at or after its hash (wrapping at the
// end of the ring). Because virtual-node positions depend only on the node
// index and not on the total node count, adding or removing a node moves only
// the services that fall into that node's ranges (~1/N) instead of remapping
// nearly every service as modulo hashing would.
//
// Every Partitioner built for the same nodeCount shares an identical ring,
// so nodes independently reach the same mapping without any coordination.
type Partitioner struct {
	nodeCount int
	nodeID    int
	ring      []ringEntry
}

// NewPartitioner creates a partitioner for the given cluster topology.
func NewPartitioner(nodeCount, nodeID int) *Partitioner {
	if nodeCount <= 0 {
		nodeCount = 1
	}
	if nodeID < 0 {
		nodeID = 0
	}
	// PartitionKey only ever returns values in [0, nodeCount-1]. A nodeID at
	// or above nodeCount would make ShouldProcess false for every service,
	// leaving the node permanently idle. Clamp it into a usable slot.
	if nodeID >= nodeCount {
		nodeID = nodeCount - 1
	}
	return &Partitioner{
	p := &Partitioner{
		nodeCount: nodeCount,
		nodeID:    nodeID,
	}
	p.ring = buildRing(nodeCount)
	return p
}

// buildRing deterministically constructs the consistent-hash ring for a
// cluster of nodeCount nodes. It depends only on nodeCount so that every node
// in the cluster builds the same ring.
func buildRing(nodeCount int) []ringEntry {
	ring := make([]ringEntry, 0, nodeCount*virtualNodesPerNode)
	for node := 0; node < nodeCount; node++ {
		for replica := 0; replica < virtualNodesPerNode; replica++ {
			key := fmt.Sprintf("node-%d-%d", node, replica)
			ring = append(ring, ringEntry{position: hashKey(key), node: node})
		}
	}
	sort.Slice(ring, func(i, j int) bool {
		if ring[i].position == ring[j].position {
			return ring[i].node < ring[j].node
		}
		return ring[i].position < ring[j].position
	})
	return ring
}

// hashKey hashes a string into a 64-bit ring position using SHA-256.
func hashKey(s string) uint64 {
	h := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(h[:8])
}

// ShouldProcess returns true if this node should process events for the given service.
func (p *Partitioner) ShouldProcess(service string) bool {
	return p.PartitionKey(service) == p.nodeID
}

// PartitionKey returns the node ID responsible for the given service.
func (p *Partitioner) PartitionKey(service string) int {
	if p.nodeCount <= 1 || len(p.ring) == 0 {
		return 0
	}
	h := hashKey(service)
	idx := sort.Search(len(p.ring), func(i int) bool {
		return p.ring[i].position >= h
	})
	if idx == len(p.ring) {
		idx = 0 // wrap around the ring
	}
	return p.ring[idx].node
}

// NodeCount returns the total number of nodes.
func (p *Partitioner) NodeCount() int {
	return p.nodeCount
}

// NodeID returns this node's ID.
func (p *Partitioner) NodeID() int {
	return p.nodeID
}

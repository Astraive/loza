package cluster

import (
	"crypto/sha256"
	"encoding/binary"
)

// Partitioner assigns services to nodes using consistent hashing.
type Partitioner struct {
	nodeCount int
	nodeID    int
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
		nodeCount: nodeCount,
		nodeID:    nodeID,
	}
}

// ShouldProcess returns true if this node should process events for the given service.
func (p *Partitioner) ShouldProcess(service string) bool {
	return p.PartitionKey(service) == p.nodeID
}

// PartitionKey returns the node ID responsible for the given service.
func (p *Partitioner) PartitionKey(service string) int {
	if p.nodeCount <= 1 {
		return 0
	}
	h := sha256.Sum256([]byte(service))
	val := binary.BigEndian.Uint32(h[:4])
	return int(val % uint32(p.nodeCount))
}

// NodeCount returns the total number of nodes.
func (p *Partitioner) NodeCount() int {
	return p.nodeCount
}

// NodeID returns this node's ID.
func (p *Partitioner) NodeID() int {
	return p.nodeID
}

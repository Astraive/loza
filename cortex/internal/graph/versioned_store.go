package graph

import (
	"context"
	"sync"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

// VersionedGraphStore tracks successful graph mutations so readers with cached
// adjacency can detect writes made through any holder of this shared store.
type VersionedGraphStore struct {
	store    storage.GraphStore
	mu       sync.RWMutex
	revision uint64
}

func NewVersionedGraphStore(store storage.GraphStore) *VersionedGraphStore {
	return &VersionedGraphStore{store: store}
}

// Version returns the number of successful mutations observed through this store.
func (s *VersionedGraphStore) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revision
}

func (s *VersionedGraphStore) SaveNode(ctx context.Context, node *models.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.SaveNode(ctx, node); err != nil {
		return err
	}
	s.revision++
	return nil
}

func (s *VersionedGraphStore) GetNode(ctx context.Context, id string) (*models.Node, error) {
	return s.store.GetNode(ctx, id)
}

func (s *VersionedGraphStore) ListNodes(ctx context.Context, nodeType string, limit int) ([]*models.Node, error) {
	return s.store.ListNodes(ctx, nodeType, limit)
}

func (s *VersionedGraphStore) SaveEdge(ctx context.Context, edge *models.Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.SaveEdge(ctx, edge); err != nil {
		return err
	}
	s.revision++
	return nil
}

func (s *VersionedGraphStore) GetEdges(ctx context.Context, nodeID string, edgeType string) ([]*models.Edge, error) {
	return s.store.GetEdges(ctx, nodeID, edgeType)
}

// WithReadLock holds the graph revision stable for the duration of fn.
func (s *VersionedGraphStore) WithReadLock(fn func(storage.GraphStore, uint64) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(s.store, s.revision)
}

func (s *VersionedGraphStore) Traverse(ctx context.Context, startNodeID string, opts models.TraversalOptions) (*models.GraphView, error) {
	return s.store.Traverse(ctx, startNodeID, opts)
}

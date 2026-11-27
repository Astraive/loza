package graph

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

// AdjacencyCache preloads edges into memory for fast in-memory BFS.
type AdjacencyCache struct {
	edges map[string][]*models.Edge // nodeID -> edges
	mu    sync.RWMutex
}

func NewAdjacencyCache() *AdjacencyCache {
	return &AdjacencyCache{
		edges: make(map[string][]*models.Edge),
	}
}

// GetEdges returns the cached edges for nodeID and whether the node has been
// loaded into the cache. The boolean is the load sentinel: it distinguishes a
// node that was never cached from one cached with no (or nil) edges, which a
// nil-slice check alone cannot do.
func (c *AdjacencyCache) GetEdges(nodeID string) ([]*models.Edge, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	edges, ok := c.edges[nodeID]
	return edges, ok
}

func (c *AdjacencyCache) SetEdges(nodeID string, edges []*models.Edge) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.edges[nodeID] = edges
}

func (c *AdjacencyCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.edges = make(map[string][]*models.Edge)
}

type Builder struct {
	graphStore      storage.GraphStore
	versionedStore  *VersionedGraphStore
	adjCache        *AdjacencyCache
	revisionMu      sync.Mutex
	cacheRevision   uint64
	defaultMaxDepth int
}

func NewBuilder(store storage.GraphStore) *Builder {
	versionedStore, ok := store.(*VersionedGraphStore)
	if !ok {
		versionedStore = NewVersionedGraphStore(store)
	}
	return &Builder{
		graphStore:      versionedStore,
		versionedStore:  versionedStore,
		adjCache:        NewAdjacencyCache(),
		cacheRevision:   versionedStore.Version(),
		defaultMaxDepth: 3,
	}
}

func (b *Builder) syncCacheRevision(revision uint64) {
	b.revisionMu.Lock()
	defer b.revisionMu.Unlock()
	if revision != b.cacheRevision {
		b.adjCache.Invalidate()
		b.cacheRevision = revision
	}
}

// WithDefaultMaxDepth sets the default traversal depth.
func (b *Builder) WithDefaultMaxDepth(depth int) *Builder {
	if depth > 0 {
		b.defaultMaxDepth = depth
	}
	return b
}

func (b *Builder) GraphStore() storage.GraphStore {
	return b.graphStore
}

func (b *Builder) AddNode(ctx context.Context, node *models.Node) error {
	if err := node.Validate(); err != nil {
		return fmt.Errorf("node validation failed: %w", err)
	}
	node.CreatedAt = time.Now()
	if err := b.graphStore.SaveNode(ctx, node); err != nil {
		return err
	}
	b.adjCache.Invalidate()
	return nil
}

func (b *Builder) AddEdge(ctx context.Context, edge *models.Edge) error {
	if err := edge.Validate(); err != nil {
		return fmt.Errorf("edge validation failed: %w", err)
	}

	_, err := b.graphStore.GetNode(ctx, edge.FromNodeID)
	if err != nil {
		return fmt.Errorf("from_node_id %s does not exist: %w", edge.FromNodeID, err)
	}

	_, err = b.graphStore.GetNode(ctx, edge.ToNodeID)
	if err != nil {
		return fmt.Errorf("to_node_id %s does not exist: %w", edge.ToNodeID, err)
	}

	edge.CreatedAt = time.Now()
	if err := b.graphStore.SaveEdge(ctx, edge); err != nil {
		return err
	}
	b.adjCache.Invalidate()
	return nil
}

func (b *Builder) GetNode(ctx context.Context, nodeID string) (*models.Node, error) {
	return b.graphStore.GetNode(ctx, nodeID)
}

func (b *Builder) GetEdges(ctx context.Context, nodeID string, edgeType string) ([]*models.Edge, error) {
	return b.graphStore.GetEdges(ctx, nodeID, edgeType)
}

func (b *Builder) TraverseGraph(ctx context.Context, startNodeID string, opts models.TraversalOptions) (*models.GraphView, error) {
	if opts.MaxDepth == 0 {
		opts.MaxDepth = b.defaultMaxDepth
	}

	var view *models.GraphView
	err := b.versionedStore.WithReadLock(func(store storage.GraphStore, revision uint64) error {
		b.syncCacheRevision(revision)
		var err error
		view, err = b.traverseGraphLocked(ctx, store, startNodeID, opts)
		return err
	})
	return view, err
}

// traverseGraphLocked reads one graph revision while the versioned store read lock is held.
func (b *Builder) traverseGraphLocked(ctx context.Context, store storage.GraphStore, startNodeID string, opts models.TraversalOptions) (*models.GraphView, error) {
	visited := make(map[string]bool)
	var queue []string
	queue = append(queue, startNodeID)

	var allNodes []*models.Node
	// Candidate edges are collected during traversal and filtered against the
	// final node set below, so an edge is never returned unless both of its
	// endpoints are part of the view.
	var candidateEdges []*models.Edge

	currentDepth := 0
	for len(queue) > 0 && currentDepth < opts.MaxDepth {
		levelSize := len(queue)
		for i := range levelSize {
			nodeID := queue[i]
			if visited[nodeID] {
				continue
			}
			visited[nodeID] = true

			node, err := store.GetNode(ctx, nodeID)
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					continue
				}
				return nil, fmt.Errorf("load graph node %q: %w", nodeID, err)
			}

			if opts.TimeWindowMin != nil && node.CreatedAt.Before(*opts.TimeWindowMin) {
				continue
			}
			if opts.TimeWindowMax != nil && node.CreatedAt.After(*opts.TimeWindowMax) {
				continue
			}

			allNodes = append(allNodes, node)

			// Use adjacency cache for edges. The boolean load sentinel tells
			// an unloaded node apart from one cached with zero edges.
			edges, ok := b.adjCache.GetEdges(nodeID)
			if !ok {
				edges, err = store.GetEdges(ctx, nodeID, "")
				if err != nil {
					return nil, fmt.Errorf("load graph edges for %q: %w", nodeID, err)
				}
				b.adjCache.SetEdges(nodeID, edges)
			}

			for _, edge := range edges {
				if len(opts.EdgeTypes) > 0 {
					found := false
					for _, t := range opts.EdgeTypes {
						if edge.Type == t {
							found = true
							break
						}
					}
					if !found {
						continue
					}
				}

				candidateEdges = append(candidateEdges, edge)

				if !visited[edge.ToNodeID] {
					queue = append(queue, edge.ToNodeID)
				}
			}
		}
		queue = queue[levelSize:]
		currentDepth++
	}

	// An edge can point at a node that is absent from the view: beyond MaxDepth,
	// excluded by the time window, or missing from the store. Drop those dangling
	// references so every returned edge resolves to a node in the view.
	nodeIDs := make(map[string]struct{}, len(allNodes))
	for _, node := range allNodes {
		nodeIDs[node.ID] = struct{}{}
	}
	var allEdges []*models.Edge
	for _, edge := range candidateEdges {
		if _, ok := nodeIDs[edge.FromNodeID]; !ok {
			continue
		}
		if _, ok := nodeIDs[edge.ToNodeID]; !ok {
			continue
		}
		allEdges = append(allEdges, edge)
	}

	return &models.GraphView{Nodes: allNodes, Edges: allEdges}, nil
}

func (b *Builder) GetServiceGraph(ctx context.Context, service string, depth int) (*models.GraphView, error) {
	if _, err := b.graphStore.GetNode(ctx, service); err != nil {
		return nil, fmt.Errorf("service graph root: %w", err)
	}
	opts := models.TraversalOptions{
		MaxDepth: depth,
	}
	return b.TraverseGraph(ctx, service, opts)
}

func (b *Builder) GetIncidentGraph(ctx context.Context, incidentID string, depth int) (*models.GraphView, error) {
	_, err := b.graphStore.GetNode(ctx, incidentID)
	if err != nil {
		return nil, fmt.Errorf("incident node not found: %w", err)
	}

	opts := models.TraversalOptions{
		MaxDepth: depth,
	}
	return b.TraverseGraph(ctx, incidentID, opts)
}

func init() {
	_ = models.NodeTypeService
}

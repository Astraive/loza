package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

type fakeGraphStore struct {
	nodes         map[string]*models.Node
	edges         map[string][]*models.Edge
	savedNodes    []*models.Node
	savedEdges    []*models.Edge
	nodeErr       error
	edgeErr       error
	saveErr       error
	getEdgesCalls int
}

func (f *fakeGraphStore) SaveNode(_ context.Context, node *models.Node) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.savedNodes = append(f.savedNodes, node)
	f.nodes[node.ID] = node
	return nil
}

func (f *fakeGraphStore) GetNode(_ context.Context, id string) (*models.Node, error) {
	if f.nodeErr != nil {
		return nil, f.nodeErr
	}
	if node, ok := f.nodes[id]; ok {
		return node, nil
	}
	return nil, storage.ErrNotFound
}

func (f *fakeGraphStore) ListNodes(_ context.Context, nodeType string, limit int) ([]*models.Node, error) {
	var out []*models.Node
	for _, node := range f.nodes {
		if nodeType == "" || string(node.Type) == nodeType {
			out = append(out, node)
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeGraphStore) SaveEdge(_ context.Context, edge *models.Edge) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.savedEdges = append(f.savedEdges, edge)
	f.edges[edge.FromNodeID] = append(f.edges[edge.FromNodeID], edge)
	return nil
}

func (f *fakeGraphStore) GetEdges(_ context.Context, nodeID string, edgeType string) ([]*models.Edge, error) {
	f.getEdgesCalls++
	if f.edgeErr != nil {
		return nil, f.edgeErr
	}
	var out []*models.Edge
	for _, edge := range f.edges[nodeID] {
		if edgeType == "" || string(edge.Type) == edgeType {
			out = append(out, edge)
		}
	}
	return out, nil
}

func (f *fakeGraphStore) Traverse(ctx context.Context, startNodeID string, opts models.TraversalOptions) (*models.GraphView, error) {
	return nil, nil
}

func TestAddNodeAndEdge(t *testing.T) {
	store := &fakeGraphStore{nodes: map[string]*models.Node{}, edges: map[string][]*models.Edge{}}
	builder := NewBuilder(store)

	node := &models.Node{ID: "svc-a", Type: models.NodeTypeService, Label: "svc-a"}
	if err := builder.AddNode(context.Background(), node); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if node.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}

	edge := &models.Edge{ID: "e1", FromNodeID: "svc-a", ToNodeID: "svc-a", Type: models.EdgeTypeDependsOn, Weight: 1}
	if err := builder.AddEdge(context.Background(), edge); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if edge.CreatedAt.IsZero() {
		t.Fatal("expected edge created_at to be set")
	}
}

func TestTraverseGraph(t *testing.T) {
	store := &fakeGraphStore{
		nodes: map[string]*models.Node{},
		edges: map[string][]*models.Edge{},
	}
	now := time.Now()
	store.nodes["n1"] = &models.Node{ID: "n1", Type: models.NodeTypeIncident, Label: "n1", CreatedAt: now}
	store.nodes["n2"] = &models.Node{ID: "n2", Type: models.NodeTypeMetric, Label: "n2", CreatedAt: now.Add(time.Second)}
	store.nodes["n3"] = &models.Node{ID: "n3", Type: models.NodeTypeLog, Label: "n3", CreatedAt: now.Add(2 * time.Second)}
	store.edges["n1"] = []*models.Edge{{ID: "e1", FromNodeID: "n1", ToNodeID: "n2", Type: models.EdgeTypeDependsOn, Weight: 1}}
	store.edges["n2"] = []*models.Edge{{ID: "e2", FromNodeID: "n2", ToNodeID: "n3", Type: models.EdgeTypeDependsOn, Weight: 1}}

	view, err := NewBuilder(store).TraverseGraph(context.Background(), "n1", models.TraversalOptions{
		MaxDepth:  2,
		EdgeTypes: []models.EdgeType{models.EdgeTypeDependsOn},
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}
	if len(view.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(view.Nodes))
	}
	// e2 targets n3, which is beyond MaxDepth and therefore not in the view;
	// returning it would leave a dangling edge reference.
	if len(view.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(view.Edges))
	}
	if view.Edges[0].ID != "e1" {
		t.Fatalf("expected edge e1, got %s", view.Edges[0].ID)
	}
	assertEdgesResolveToNodes(t, view)
}

// assertEdgesResolveToNodes verifies that every edge in a view references
// nodes that are also present in that view.
func assertEdgesResolveToNodes(t *testing.T, view *models.GraphView) {
	t.Helper()
	nodeIDs := make(map[string]struct{}, len(view.Nodes))
	for _, node := range view.Nodes {
		nodeIDs[node.ID] = struct{}{}
	}
	for _, edge := range view.Edges {
		for _, endpoint := range []string{edge.FromNodeID, edge.ToNodeID} {
			if _, ok := nodeIDs[endpoint]; !ok {
				t.Errorf("edge %q references node %q which is not in the view", edge.ID, endpoint)
			}
		}
	}
}

func TestTraverseGraphOmitsDanglingEdges(t *testing.T) {
	now := time.Now()
	windowMin := now.Add(-time.Minute)
	store := &fakeGraphStore{
		nodes: map[string]*models.Node{
			"n1":  {ID: "n1", Type: models.NodeTypeIncident, Label: "n1", CreatedAt: now},
			"n2":  {ID: "n2", Type: models.NodeTypeMetric, Label: "n2", CreatedAt: now.Add(time.Second)},
			"n3":  {ID: "n3", Type: models.NodeTypeLog, Label: "n3", CreatedAt: now.Add(2 * time.Second)},
			"old": {ID: "old", Type: models.NodeTypeLog, Label: "old", CreatedAt: now.Add(-time.Hour)},
		},
		edges: map[string][]*models.Edge{
			"n1": {
				{ID: "e1", FromNodeID: "n1", ToNodeID: "n2", Type: models.EdgeTypeDependsOn, Weight: 1},
				{ID: "e-old", FromNodeID: "n1", ToNodeID: "old", Type: models.EdgeTypeDependsOn, Weight: 1},
				{ID: "e-missing", FromNodeID: "n1", ToNodeID: "missing", Type: models.EdgeTypeDependsOn, Weight: 1},
			},
			"n2": {
				{ID: "e2", FromNodeID: "n2", ToNodeID: "n3", Type: models.EdgeTypeDependsOn, Weight: 1},
			},
		},
	}

	view, err := NewBuilder(store).TraverseGraph(context.Background(), "n1", models.TraversalOptions{
		MaxDepth:      2,
		TimeWindowMin: &windowMin,
	})
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(view.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(view.Nodes))
	}
	if len(view.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(view.Edges))
	}
	if view.Edges[0].ID != "e1" {
		t.Fatalf("expected edge e1, got %s", view.Edges[0].ID)
	}
	assertEdgesResolveToNodes(t, view)
}

func TestGraphLookupPreservesNotFoundAndStorageErrors(t *testing.T) {
	notFound := NewBuilder(&fakeGraphStore{
		nodes: map[string]*models.Node{},
		edges: map[string][]*models.Edge{},
	})
	if _, err := notFound.GetServiceGraph(context.Background(), "missing", 3); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected typed not-found error, got %v", err)
	}

	storageFailure := errors.New("graph database unavailable")
	failing := NewBuilder(&fakeGraphStore{
		nodes:   map[string]*models.Node{},
		edges:   map[string][]*models.Edge{},
		nodeErr: storageFailure,
	})
	if _, err := failing.GetIncidentGraph(context.Background(), "inc-1", 3); !errors.Is(err, storageFailure) {
		t.Fatalf("expected storage error propagation, got %v", err)
	}
}

// Regression test: a traversal performed after AddEdge must observe the new
// edge instead of the edge list cached by an earlier traversal.
func TestTraverseGraphSeesEdgesAddedAfterFirstTraversal(t *testing.T) {
	store := &fakeGraphStore{
		nodes: map[string]*models.Node{},
		edges: map[string][]*models.Edge{},
	}
	store.nodes["n1"] = &models.Node{ID: "n1", Type: models.NodeTypeService, Label: "n1"}
	store.nodes["n2"] = &models.Node{ID: "n2", Type: models.NodeTypeService, Label: "n2"}
	store.nodes["n3"] = &models.Node{ID: "n3", Type: models.NodeTypeService, Label: "n3"}
	store.edges["n1"] = []*models.Edge{{ID: "e1", FromNodeID: "n1", ToNodeID: "n2", Type: models.EdgeTypeDependsOn, Weight: 1}}

	builder := NewBuilder(store)
	// Depth 2 keeps both endpoints of each edge in the view. With the
	// dangling-edge filter, a depth-1 traversal would drop edges pointing past
	// the view before the cache behavior could be observed.
	opts := models.TraversalOptions{MaxDepth: 2}

	first, err := builder.TraverseGraph(context.Background(), "n1", opts)
	if err != nil {
		t.Fatalf("first traverse: %v", err)
	}
	if len(first.Edges) != 1 {
		t.Fatalf("first traverse: expected 1 edge, got %d", len(first.Edges))
	}
	if got, ok := builder.adjCache.GetEdges("n1"); !ok || len(got) != 1 {
		t.Fatalf("expected n1 edges to be cached after first traverse, ok=%v got %d", ok, len(got))
	}

	newEdge := &models.Edge{ID: "e2", FromNodeID: "n1", ToNodeID: "n3", Type: models.EdgeTypeDependsOn, Weight: 1}
	if err := builder.AddEdge(context.Background(), newEdge); err != nil {
		t.Fatalf("add edge: %v", err)
	}

	second, err := builder.TraverseGraph(context.Background(), "n1", opts)
	if err != nil {
		t.Fatalf("second traverse: %v", err)
	}
	if len(second.Edges) != 2 {
		t.Fatalf("second traverse: expected 2 edges after AddEdge, got %d (stale adjacency cache)", len(second.Edges))
	}
}

// Regression test: both write paths invalidate the adjacency cache, and a
// failed write leaves a previously populated cache untouched.
func TestGraphWritesInvalidateAdjacencyCache(t *testing.T) {
	ctx := context.Background()
	store := &fakeGraphStore{
		nodes: map[string]*models.Node{},
		edges: map[string][]*models.Edge{},
	}
	store.nodes["n1"] = &models.Node{ID: "n1", Type: models.NodeTypeService, Label: "n1"}
	store.edges["n1"] = []*models.Edge{{ID: "e1", FromNodeID: "n1", ToNodeID: "n2", Type: models.EdgeTypeDependsOn, Weight: 1}}
	builder := NewBuilder(store)

	populate := func(expected int) {
		t.Helper()
		if _, err := builder.TraverseGraph(ctx, "n1", models.TraversalOptions{MaxDepth: 1}); err != nil {
			t.Fatalf("traverse: %v", err)
		}
		if got, ok := builder.adjCache.GetEdges("n1"); !ok || len(got) != expected {
			t.Fatalf("expected populated adjacency cache with %d edges, ok=%v got %d", expected, ok, len(got))
		}
	}

	populate(1)
	if err := builder.AddNode(ctx, &models.Node{ID: "n2", Type: models.NodeTypeService, Label: "n2"}); err != nil {
		t.Fatalf("add node: %v", err)
	}
	if got, ok := builder.adjCache.GetEdges("n1"); ok || got != nil {
		t.Fatalf("expected AddNode to invalidate adjacency cache, ok=%v got %d edges", ok, len(got))
	}

	populate(1)
	if err := builder.AddEdge(ctx, &models.Edge{ID: "e2", FromNodeID: "n1", ToNodeID: "n2", Type: models.EdgeTypeDependsOn, Weight: 1}); err != nil {
		t.Fatalf("add edge: %v", err)
	}
	if got, ok := builder.adjCache.GetEdges("n1"); ok || got != nil {
		t.Fatalf("expected AddEdge to invalidate adjacency cache, ok=%v got %d edges", ok, len(got))
	}

	populate(2)
	store.saveErr = errors.New("graph store write failed")
	if err := builder.AddNode(ctx, &models.Node{ID: "n3", Type: models.NodeTypeService, Label: "n3"}); err == nil {
		t.Fatal("expected AddNode to propagate the storage error")
	}
	if got, ok := builder.adjCache.GetEdges("n1"); !ok || len(got) != 2 {
		t.Fatalf("expected failed AddNode to preserve adjacency cache, ok=%v got %d edges", ok, len(got))
	}
}

// TestAdjacencyCacheLoadSentinel verifies that the cache reports whether a
// node has been loaded independently of whether its cached edge slice is nil,
// so "unloaded" is distinguishable from "loaded with zero edges".
func TestAdjacencyCacheLoadSentinel(t *testing.T) {
	c := NewAdjacencyCache()

	if _, ok := c.GetEdges("missing"); ok {
		t.Fatal("unloaded node must not be reported as loaded")
	}

	c.SetEdges("empty", nil)
	if edges, ok := c.GetEdges("empty"); !ok {
		t.Fatal("node cached with nil edges must still be reported as loaded")
	} else if edges != nil {
		t.Fatalf("expected nil edges, got %v", edges)
	}

	c.SetEdges("populated", []*models.Edge{{ID: "e1"}})
	if edges, ok := c.GetEdges("populated"); !ok || len(edges) != 1 {
		t.Fatalf("expected 1 cached edge, got ok=%v edges=%v", ok, edges)
	}

	c.Invalidate()
	if _, ok := c.GetEdges("empty"); ok {
		t.Fatal("invalidated node must not be reported as loaded")
	}
}

// TestTraverseGraphCachesEmptyEdgeResults reproduces the reported defect: a
// node whose store returns a nil/empty edge slice was re-fetched on every
// traversal because the old "edges == nil" check could not distinguish a cache
// miss from a cached empty result. With the load sentinel, the store is hit
// only once across repeated traversals using the same builder.
func TestTraverseGraphCachesEmptyEdgeResults(t *testing.T) {
	store := &fakeGraphStore{
		nodes: map[string]*models.Node{},
		edges: map[string][]*models.Edge{},
	}
	now := time.Now()
	store.nodes["n1"] = &models.Node{ID: "n1", Type: models.NodeTypeService, Label: "n1", CreatedAt: now}
	// Intentionally no edges for "n1": GetEdges returns a nil slice.

	builder := NewBuilder(store)
	for i := 0; i < 2; i++ {
		view, err := builder.TraverseGraph(context.Background(), "n1", models.TraversalOptions{MaxDepth: 1})
		if err != nil {
			t.Fatalf("traverse #%d: %v", i+1, err)
		}
		if len(view.Nodes) != 1 {
			t.Fatalf("traverse #%d: expected 1 node, got %d", i+1, len(view.Nodes))
		}
		if len(view.Edges) != 0 {
			t.Fatalf("traverse #%d: expected 0 edges, got %d", i+1, len(view.Edges))
		}
	}

	if store.getEdgesCalls != 1 {
		t.Fatalf("expected empty edge result to be cached after the first traversal (1 store call), got %d", store.getEdgesCalls)
	}
}

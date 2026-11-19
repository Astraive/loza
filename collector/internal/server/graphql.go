package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/astraive/loza/collector/internal/auth"
	"github.com/graphql-go/graphql"
)

const (
	defaultGraphQLDepthLimit = 10
	defaultGraphQLBatchLimit = 10
	maxGraphQLDepth = 10

	// These mirror the collector HTTP server defaults (see HTTPConfig and the
	// default collector config) so the GraphQL endpoint is not left unbounded.
	graphQLReadHeaderTimeout = 5 * time.Second
	graphQLReadTimeout       = 30 * time.Second
	graphQLWriteTimeout      = 60 * time.Second
	graphQLIdleTimeout       = 90 * time.Second
	maxGraphQLBodyBytes      = 10 << 20 // 10 MiB
)

type GraphQLServer struct {
	cfg               GraphQLConfig
	state             State
	ready             atomic.Bool
	server            *http.Server
	authEnabled       bool
	allowLocalDevKeys bool
	keyStore          auth.KeyStore
	keyCache          *auth.MemoryKeyCache
	serverSecret      []byte
	depthLimit        int
	batchLimit        int
}

func NewGraphQLServer(cfg GraphQLConfig, state State) *GraphQLServer {
	depthLimit := cfg.DepthLimit
	if depthLimit <= 0 {
		depthLimit = defaultGraphQLDepthLimit
	}
	batchLimit := cfg.BatchLimit
	if batchLimit <= 0 {
		batchLimit = defaultGraphQLBatchLimit
	}
	return &GraphQLServer{
		cfg:        cfg,
		state:      state,
		depthLimit: depthLimit,
		batchLimit: batchLimit,
	}
}

// WithAuth configures API key authentication for the GraphQL server.
// When set, all requests to the GraphQL endpoint are authenticated.
func (s *GraphQLServer) WithAuth(store auth.KeyStore, cache *auth.MemoryKeyCache, serverSecret []byte) *GraphQLServer {
	s.authEnabled = true
	s.keyStore = store
	s.keyCache = cache
	s.serverSecret = serverSecret
	return s
}

// WithAllowLocalDevKeys enables lz_local_dev_* key acceptance on this server.
func (s *GraphQLServer) WithAllowLocalDevKeys(v bool) *GraphQLServer {
	s.allowLocalDevKeys = v
	return s
}

func (s *GraphQLServer) Name() string { return "graphql" }

func (s *GraphQLServer) Addr() string { return s.cfg.Port }

func (s *GraphQLServer) IsReady() bool { return s.ready.Load() }

func (s *GraphQLServer) Start(ctx context.Context) error {
	if !s.cfg.Enabled {
		return nil
	}

	metricsType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Metrics",
		Fields: graphql.Fields{
			"requestsTotal":   &graphql.Field{Type: graphql.Int},
			"requestsAuthErr": &graphql.Field{Type: graphql.Int},
			"requestsLimited": &graphql.Field{Type: graphql.Int},
			"eventsAccepted":  &graphql.Field{Type: graphql.Int},
			"eventsInvalid":   &graphql.Field{Type: graphql.Int},
			"eventsRejected":  &graphql.Field{Type: graphql.Int},
			"eventsDeduped":   &graphql.Field{Type: graphql.Int},
		},
	})

	queryType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"health": &graphql.Field{
				Type: graphql.Boolean,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return s.state.IsHealthy(), nil
				},
			},
			"ready": &graphql.Field{
				Type: graphql.Boolean,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return s.state.IsReady(), nil
				},
			},
			"metrics": &graphql.Field{
				Type: metricsType,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					m := s.state.GetMetrics()
					return map[string]interface{}{
						"requestsTotal":   m.RequestsTotal,
						"requestsAuthErr": m.RequestsAuthErr,
						"requestsLimited": m.RequestsLimited,
						"eventsAccepted":  m.EventsAccepted,
						"eventsInvalid":   m.EventsInvalid,
						"eventsRejected":  m.EventsRejected,
						"eventsDeduped":   m.EventsDeduped,
					}, nil
				},
			},
		},
	})

	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: queryType,
	})
	if err != nil {
		return err
	}

	handler := s.handler(schema)
	handler := s.newHandler(schema)
	s.server = s.newServer(handler)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()

	lis, err := net.Listen("tcp", s.cfg.Port)
	if err != nil {
		return err
	}

	s.ready.Store(true)
	return s.server.Serve(lis)
}

// newHandler builds the GraphQL request handler, including authentication and
// a hard cap on the request body size so oversized payloads cannot be decoded.
func (s *GraphQLServer) newHandler(schema graphql.Schema) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxGraphQLBodyBytes)

		var req struct {
			Query         string                 `json:"query"`
			OperationName string                 `json:"operationName"`
			Variables     map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if isIntrospectionQuery(req.Query) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "introspection_disabled"})
			return
		}

		if queryDepth(req.Query) > maxGraphQLDepth {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "query_exceeds_maximum_depth"})
			return
		}

		result := graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  req.Query,
			VariableValues: req.Variables,
			OperationName:  req.OperationName,
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})

	var handler http.Handler = mux
	if s.authEnabled {
		authMW := auth.Middleware(s.keyStore, s.keyCache, s.serverSecret, auth.WithAllowLocalDevKeys(s.allowLocalDevKeys))
		handler = authMW(handler)
	}
	return handler
}

// newServer builds the HTTP server with bounded timeouts so slow or idle
// clients cannot hold connections open indefinitely.
func (s *GraphQLServer) newServer(handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              s.cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: graphQLReadHeaderTimeout,
		ReadTimeout:       graphQLReadTimeout,
		WriteTimeout:      graphQLWriteTimeout,
		IdleTimeout:       graphQLIdleTimeout,
	}
}

func (s *GraphQLServer) Stop(ctx context.Context) error {
	s.ready.Store(false)
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// handler builds the GraphQL HTTP handler for the given schema, applying the
// configured depth and batch limits enforced in serveGraphQL.
func (s *GraphQLServer) handler(schema graphql.Schema) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		s.serveGraphQL(w, r, schema)
	})

	var handler http.Handler = mux
	if s.authEnabled {
		authMW := auth.Middleware(s.keyStore, s.keyCache, s.serverSecret, auth.WithAllowLocalDevKeys(s.allowLocalDevKeys))
		handler = authMW(handler)
	}
	return handler
}

type graphQLRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

// serveGraphQL handles a single GraphQL request or a batch of them. A batch is
// a JSON array of request objects; its size is capped by the configured batch
// limit. Each operation is subject to the configured depth limit.
func (s *GraphQLServer) serveGraphQL(w http.ResponseWriter, r *http.Request, schema graphql.Schema) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	trimmed := bytes.TrimSpace(body)

	isBatch := len(trimmed) > 0 && trimmed[0] == '['
	var requests []graphQLRequest
	if isBatch {
		if err := json.Unmarshal(trimmed, &requests); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		if len(requests) > s.batchLimit {
			writeGraphQLError(w, http.StatusBadRequest, "batch_exceeds_maximum_size")
			return
		}
	} else {
		var single graphQLRequest
		if err := json.Unmarshal(trimmed, &single); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		requests = []graphQLRequest{single}
	}

	results := make([]*graphql.Result, 0, len(requests))
	for _, req := range requests {
		if isIntrospectionQuery(req.Query) {
			writeGraphQLError(w, http.StatusForbidden, "introspection_disabled")
			return
		}
		if queryDepth(req.Query) > s.depthLimit {
			writeGraphQLError(w, http.StatusBadRequest, "query_exceeds_maximum_depth")
			return
		}
		results = append(results, graphql.Do(graphql.Params{
			Schema:         schema,
			RequestString:  req.Query,
			VariableValues: req.Variables,
			OperationName:  req.OperationName,
		}))
	}

	w.Header().Set("Content-Type", "application/json")
	if isBatch {
		_ = json.NewEncoder(w).Encode(results)
	} else {
		_ = json.NewEncoder(w).Encode(results[0])
	}
}

func writeGraphQLError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code})
}

// isIntrospectionQuery checks if the query attempts GraphQL introspection.
func isIntrospectionQuery(query string) bool {
	return containsWord(query, "__schema") ||
		containsWord(query, "__type") ||
		containsWord(query, "__typename")
}

func containsWord(s, word string) bool {
	for i := 0; i <= len(s)-len(word); i++ {
		if i > 0 && isAlpha(rune(s[i-1])) {
			continue
		}
		if i+len(word) < len(s) && isAlpha(rune(s[i+len(word)])) {
			continue
		}
		if s[i:i+len(word)] == word {
			return true
		}
	}
	return false
}

func isAlpha(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// queryDepth counts the maximum nesting depth of curly braces in a GraphQL query.
func queryDepth(query string) int {
	maxDepth := 0
	depth := 0
	for _, c := range query {
		switch c {
		case '{':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case '}':
			depth--
		}
	}
	return maxDepth
}

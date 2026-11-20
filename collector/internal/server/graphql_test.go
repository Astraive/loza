package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGraphQLSchema(t *testing.T) graphql.Schema {
	t.Helper()
	query := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"health": &graphql.Field{
				Type: graphql.Boolean,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return true, nil
				},
			},
		},
	})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: query})
	require.NoError(t, err)
	return schema
}

func doGraphQLRequest(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertGraphQLErrorCode(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, rec.Code)
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, code, body.Error)
}

// TestGraphQLLimitDefaults verifies that NewGraphQLServer reads DepthLimit and
// BatchLimit from config, falling back to safe defaults for unset/invalid values.
func TestGraphQLLimitDefaults(t *testing.T) {
	tests := []struct {
		name      string
		cfg       GraphQLConfig
		wantDepth int
		wantBatch int
	}{
		{"unset uses defaults", GraphQLConfig{}, defaultGraphQLDepthLimit, defaultGraphQLBatchLimit},
		{"configured values used", GraphQLConfig{DepthLimit: 15, BatchLimit: 25}, 15, 25},
		{"non-positive falls back", GraphQLConfig{DepthLimit: -1, BatchLimit: -5}, defaultGraphQLDepthLimit, defaultGraphQLBatchLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewGraphQLServer(tt.cfg, nil)
			assert.Equal(t, tt.wantDepth, srv.depthLimit)
			assert.Equal(t, tt.wantBatch, srv.batchLimit)
		})
	}
}

// TestIsIntrospectionQuery guards the word-boundary scan used before the depth
// check; it must not panic at end-of-string and must respect word boundaries.
func TestIsIntrospectionQuery(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{`{ health }`, false},
		{`{ a { b { c } } }`, false},
		{`{ __schema { types { name } } }`, true},
		{`{ __typename }`, true},
		{`{ health__schema }`, false},
		{`{ __schemaExtra }`, false},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			assert.Equal(t, tt.want, isIntrospectionQuery(tt.query))
		})
	}
}

// TestGraphQLConfiguredDepthLimit is a regression test: a query deeper than the
// configured limit is rejected, even though it is below the old hardcoded value
// of 10. This fails against the original implementation that ignored the option.
func TestGraphQLConfiguredDepthLimit(t *testing.T) {
	srv := NewGraphQLServer(GraphQLConfig{DepthLimit: 2}, nil)
	h := srv.handler(testGraphQLSchema(t))

	rec := doGraphQLRequest(t, h, `{"query":"{ a { b { c } } }"}`)
	assertGraphQLErrorCode(t, rec, http.StatusBadRequest, "query_exceeds_maximum_depth")

	rec = doGraphQLRequest(t, h, `{"query":"{ health }"}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	// A higher configured limit permits queries above the old hardcoded value.
	srv = NewGraphQLServer(GraphQLConfig{DepthLimit: 5}, nil)
	h = srv.handler(testGraphQLSchema(t))
	rec = doGraphQLRequest(t, h, `{"query":"{ a { b { c } } }"}`)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestGraphQLConfiguredBatchLimit verifies that the configured BatchLimit caps
// the number of operations in a batched request while single requests keep their
// original object-shaped response.
func TestGraphQLConfiguredBatchLimit(t *testing.T) {
	srv := NewGraphQLServer(GraphQLConfig{BatchLimit: 2}, nil)
	h := srv.handler(testGraphQLSchema(t))

	rec := doGraphQLRequest(t, h, `[{"query":"{ health }"},{"query":"{ health }"},{"query":"{ health }"}]`)
	assertGraphQLErrorCode(t, rec, http.StatusBadRequest, "batch_exceeds_maximum_size")

	rec = doGraphQLRequest(t, h, `[{"query":"{ health }"},{"query":"{ health }"}]`)
	require.Equal(t, http.StatusOK, rec.Code)
	var results []json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &results))
	assert.Len(t, results, 2)

	rec = doGraphQLRequest(t, h, `{"query":"{ health }"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	var single map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &single))
}

func TestGraphQLServerHTTPServerTimeouts(t *testing.T) {
	srv := NewGraphQLServer(GraphQLConfig{Enabled: true, Port: ":9310"}, &mockState{})

	s := srv.newServer(http.NotFoundHandler())

	require.NotNil(t, s)
	assert.Equal(t, ":9310", s.Addr)
	assert.Equal(t, graphQLReadHeaderTimeout, s.ReadHeaderTimeout)
	assert.Equal(t, graphQLReadTimeout, s.ReadTimeout)
	assert.Equal(t, graphQLWriteTimeout, s.WriteTimeout)
	assert.Equal(t, graphQLIdleTimeout, s.IdleTimeout)

	// The defect was that all of these were left at zero (unbounded).
	assert.Greater(t, s.ReadHeaderTimeout, time.Duration(0))
	assert.Greater(t, s.ReadTimeout, time.Duration(0))
	assert.Greater(t, s.WriteTimeout, time.Duration(0))
	assert.Greater(t, s.IdleTimeout, time.Duration(0))
}

func TestGraphQLServerRejectsOversizedBody(t *testing.T) {
	schema, err := graphql.NewSchema(graphql.SchemaConfig{
		Query: graphql.NewObject(graphql.ObjectConfig{
			Name: "Query",
			Fields: graphql.Fields{
				"h": &graphql.Field{
					Type: graphql.Boolean,
					Resolve: func(p graphql.ResolveParams) (interface{}, error) {
						return true, nil
					},
				},
			},
		}),
	})
	require.NoError(t, err)

	srv := NewGraphQLServer(GraphQLConfig{Enabled: true, Port: "127.0.0.1:0"}, &mockState{})
	ts := httptest.NewServer(srv.newHandler(schema))
	defer ts.Close()

	t.Run("oversized body is rejected", func(t *testing.T) {
		body := `{"query":"{h}","padding":"` + strings.Repeat("x", maxGraphQLBodyBytes) + `"}`
		resp, err := http.Post(ts.URL+"/graphql", "application/json", strings.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	})

	t.Run("normal body still succeeds", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/graphql", "application/json", strings.NewReader(`{"query":"{h}"}`))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

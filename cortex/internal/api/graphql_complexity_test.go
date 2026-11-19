package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalyzeGraphQLQuerySimple(t *testing.T) {
	cost, err := analyzeGraphQLQuery(`query { incident(id: "x") { id status } }`, "")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cost.depth != 2 {
		t.Fatalf("depth = %d, want 2", cost.depth)
	}
	if cost.complexity != 3 {
		t.Fatalf("complexity = %d, want 3", cost.complexity)
	}
}

// A wide request stays shallow in the request text, so the old brace counter
// let it through. Each aliased field is real resolver work and must be counted.
func TestAnalyzeGraphQLQueryAliasFanOut(t *testing.T) {
	var q strings.Builder
	q.WriteString("query {\n")
	for i := range 100 {
		q.WriteString("  a" + itoa(i) + `: incident(id: "x") { id status severity }` + "\n")
	}
	q.WriteString("}\n")

	cost, err := analyzeGraphQLQuery(q.String(), "")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cost.depth > maxGraphQLDepth {
		t.Fatalf("depth %d unexpectedly exceeds the depth cap", cost.depth)
	}
	// 100 aliases x (1 field + 3 subfields) = 400 selections. The analyzer
	// stops counting once it is certain the cap is breached, so the reported
	// value saturates just above the limit rather than reaching 400.
	if cost.complexity <= maxGraphQLComplexity {
		t.Fatalf("complexity = %d, want > %d (alias fan-out must be rejected)", cost.complexity, maxGraphQLComplexity)
	}
}

// Repeated fragment spreads are another way to multiply work without adding
// textual depth. The analyzer expands each spread in place.
func TestAnalyzeGraphQLQueryFragmentFanOut(t *testing.T) {
	var q strings.Builder
	q.WriteString("query {\n")
	for range 100 {
		q.WriteString("  ...Fields\n")
	}
	q.WriteString("}\nfragment Fields on Query { incident(id: \"x\") { id status severity } }\n")

	cost, err := analyzeGraphQLQuery(q.String(), "")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cost.complexity <= maxGraphQLComplexity {
		t.Fatalf("complexity = %d, want > %d (fragment fan-out must be rejected)", cost.complexity, maxGraphQLComplexity)
	}
}

func TestAnalyzeGraphQLQueryDeepNesting(t *testing.T) {
	const levels = 12
	var q strings.Builder
	q.WriteString("query { reconstruct(id: \"x\") {")
	for i := 0; i < levels; i++ {
		q.WriteString(" child {")
	}
	q.WriteString(" id ")
	for i := 0; i < levels+1; i++ {
		q.WriteString("}")
	}
	q.WriteString(" }")

	cost, err := analyzeGraphQLQuery(q.String(), "")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cost.depth <= maxGraphQLDepth {
		t.Fatalf("depth = %d, want > %d", cost.depth, maxGraphQLDepth)
	}
}

// Fragment cycles must not hang the analyzer. graphql-go rejects them at
// execution time; here we only require the walk to terminate.
func TestAnalyzeGraphQLQueryFragmentCycle(t *testing.T) {
	q := `query { ...A } fragment A on Query { ...B } fragment B on Query { ...A }`
	if _, err := analyzeGraphQLQuery(q, ""); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
}

func TestAnalyzeGraphQLQueryOperationName(t *testing.T) {
	q := `query Cheap { incident(id: "x") { id } } ` +
		`query Expensive { a: incident(id: "x") { id status } b: incident(id: "y") { id status } }`

	cost, err := analyzeGraphQLQuery(q, "Cheap")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cost.complexity != 2 {
		t.Fatalf("complexity = %d, want 2 for Cheap operation", cost.complexity)
	}

	cost, err = analyzeGraphQLQuery(q, "Expensive")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cost.complexity != 6 {
		t.Fatalf("complexity = %d, want 6 for Expensive operation", cost.complexity)
	}
}

func TestGraphQLHandlerRejectsFanOut(t *testing.T) {
	srv := &GraphQLServer{}
	if err := srv.initSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	var q strings.Builder
	q.WriteString("query {\n")
	for i := range 100 {
		q.WriteString("  a" + itoa(i) + `: incident(id: "x") { id status severity }` + "\n")
	}
	q.WriteString("}\n")

	rec := httptest.NewRecorder()
	body := fmt.Sprintf(`{"query": %q}`, q.String())
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "maximum complexity") {
		t.Fatalf("body does not report the complexity limit: %s", rec.Body.String())
	}
}

// itoa formats a small non-negative integer without pulling in strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

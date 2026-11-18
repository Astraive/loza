package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/matcher"
	"github.com/astraive/loza/cortex/internal/middleware"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/processor"
	"github.com/astraive/loza/cortex/internal/storage"
)

func TestServerHealthAndReadiness(t *testing.T) {
	srv := &Server{config: &config.Config{}, ready: true}

	rec := httptest.NewRecorder()
	srv.Healthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected healthz ok, got %d", rec.Code)
	}

	// With nil graph/processor/incidents, readyz should return not_ready
	rec = httptest.NewRecorder()
	srv.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected not ready (nil deps), got %d", rec.Code)
	}

	srv.ready = false
	rec = httptest.NewRecorder()
	srv.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected not ready, got %d", rec.Code)
	}
}

func TestRouterServesHealthz(t *testing.T) {
	srv := &Server{config: &config.Config{}, ready: true}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected router healthz ok, got %d", rec.Code)
	}
}

func TestNewServerDoesNotStartUnownedWorkers(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.DuckDB.Path = filepath.Join(t.TempDir(), "cortex.duckdb")
	stor, err := storage.NewStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stor.Close()

	before := runtime.NumGoroutine()
	for range 3 {
		_ = NewServer(cfg, stor)
	}
	time.Sleep(20 * time.Millisecond)
	if delta := runtime.NumGoroutine() - before; delta > 2 {
		t.Fatalf("server construction leaked %d background workers", delta)
	}
}

func TestReadyzDoesNotExposeStorageErrors(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.DuckDB.Path = filepath.Join(t.TempDir(), "private-cortex.duckdb")
	stor, err := storage.NewStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, stor)
	if err := stor.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Readyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected not ready after storage close, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "private-cortex.duckdb") || strings.Contains(strings.ToLower(body), "closed") {
		t.Fatalf("readiness response exposed storage details: %s", body)
	}
	if !strings.Contains(body, `"storage":"unavailable"`) {
		t.Fatalf("readiness response omitted stable storage status: %s", body)
	}
}

func TestGraphLookupStatusDistinguishesNotFoundFromStorageFailure(t *testing.T) {
	if got := graphLookupStatus(fmt.Errorf("lookup: %w", storage.ErrNotFound)); got != http.StatusNotFound {
		t.Fatalf("not-found status = %d, want 404", got)
	}
	if got := graphLookupStatus(errors.New("database unavailable")); got != http.StatusInternalServerError {
		t.Fatalf("storage failure status = %d, want 500", got)
	}
}

func TestGraphDepthValidatesInclusiveRange(t *testing.T) {
	tests := []struct {
		query string
		want  int
		ok    bool
	}{
		{query: "", want: 3, ok: true},
		{query: "?depth=1", want: 1, ok: true},
		{query: fmt.Sprintf("?depth=%d", maxGraphDepth), want: maxGraphDepth, ok: true},
		{query: "?depth=0"},
		{query: "?depth=-1"},
		{query: fmt.Sprintf("?depth=%d", maxGraphDepth+1)},
		{query: "?depth=invalid"},
	}
	for _, test := range tests {
		req := httptest.NewRequest(http.MethodGet, "/graphs/service"+test.query, nil)
		got, err := graphDepth(req)
		if test.ok {
			if err != nil || got != test.want {
				t.Errorf("query %q: depth=%d err=%v, want %d", test.query, got, err, test.want)
			}
		} else if err == nil {
			t.Errorf("query %q: expected validation error, got depth %d", test.query, got)
		}
	}
}

func TestIngestEventClassifiesStorageFailuresAsServerErrors(t *testing.T) {
	cfg := config.Default()
	cfg.Storage.DuckDB.Path = filepath.Join(t.TempDir(), "cortex.duckdb")
	stor, err := storage.NewStorage(cfg)
	if err != nil {
		t.Fatal(err)
	}
	eventProcessor := processor.NewEventProcessor(stor.Events(), stor.Topology(), stor.Graph())
	if err := stor.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(models.Event{
		ID: "evt-storage", Timestamp: time.Now(), Service: "api",
		Kind: models.EventKindLog, Provenance: "loza",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	(&Server{processor: eventProcessor}).IngestEvent(
		rec,
		httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body)),
	)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected storage failure to return 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIngestBatchClassifiesValidationFailuresAsClientErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Server{processor: processor.NewEventProcessor(nil, nil, nil)}).IngestBatch(
		rec,
		httptest.NewRequest(http.MethodPost, "/events/batch", strings.NewReader(`{"events":[{}]}`)),
	)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected validation failure to return 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

type graphQLSignatureStore struct {
	signatures map[string]*models.IncidentSignature
	getErr     error
}

func (*graphQLSignatureStore) Save(context.Context, *models.IncidentSignature) error { return nil }
func (s *graphQLSignatureStore) Get(_ context.Context, id string) (*models.IncidentSignature, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	signature := s.signatures[id]
	if signature == nil {
		return nil, storage.ErrNotFound
	}
	return signature, nil
}
func (s *graphQLSignatureStore) List(context.Context, int) ([]*models.IncidentSignature, error) {
	result := make([]*models.IncidentSignature, 0, len(s.signatures))
	for _, signature := range s.signatures {
		result = append(result, signature)
	}
	return result, nil
}
func (*graphQLSignatureStore) FindSimilar(context.Context, *models.IncidentSignature, int) ([]*models.SimilarIncident, error) {
	return nil, nil
}
func (*graphQLSignatureStore) FindByBehavioralHash(context.Context, string) ([]*models.IncidentSignature, error) {
	return nil, nil
}
func (*graphQLSignatureStore) UpdateDecay(context.Context, string, float64) error { return nil }
func (*graphQLSignatureStore) ArchiveStale(context.Context, float64) (int, error) {
	return 0, nil
}
func (*graphQLSignatureStore) UpdateLastMatched(context.Context, string) error { return nil }

func testGraphQLServer(t *testing.T) *GraphQLServer {
	t.Helper()
	server := &GraphQLServer{
		signatures: &graphQLSignatureStore{signatures: map[string]*models.IncidentSignature{
			"sig-1": {SignatureID: "sig-1", Shape: "first"},
			"sig-2": {SignatureID: "sig-2", Shape: "second"},
		}},
	}
	if err := server.initSchema(); err != nil {
		t.Fatal(err)
	}
	return server
}

func TestGraphQLExecutesNamedOperationAliasesAndSelectionSets(t *testing.T) {
	result := testGraphQLServer(t).executeQuery(
		context.Background(),
		`query First { ignored: signature(id: "sig-1") { signature_id shape } }
		 query Second { chosen: signature(id: "sig-2") { shape } }`,
		nil,
		"Second",
	)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", result.Errors)
	}
	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected GraphQL data: %#v", result.Data)
	}
	chosen, ok := data["chosen"].(map[string]interface{})
	if !ok || chosen["shape"] != "second" {
		t.Fatalf("unexpected aliased selection: %#v", data)
	}
	if _, exists := chosen["signature_id"]; exists {
		t.Fatalf("executor returned an unrequested field: %#v", chosen)
	}
	if _, exists := data["ignored"]; exists {
		t.Fatalf("executor ran an unselected operation: %#v", data)
	}
}

func TestGraphQLDoesNotDispatchFromStringArguments(t *testing.T) {
	result := testGraphQLServer(t).executeQuery(
		context.Background(),
		`query { signature(id: "ingestEvent") { shape } }`,
		nil,
		"",
	)
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0].Message, "not found") {
		t.Fatalf("expected signature lookup error, got %+v", result.Errors)
	}
}

func TestGraphQLDoesNotExposeResolverErrors(t *testing.T) {
	server := testGraphQLServer(t)
	server.signatures.(*graphQLSignatureStore).getErr = errors.New("duckdb /srv/private/cortex.duckdb is locked")
	result := server.executeQuery(
		context.Background(),
		`query { signature(id: "sig-1") { shape } }`,
		nil,
		"",
	)
	if len(result.Errors) != 1 || result.Errors[0].Message != "internal error" {
		t.Fatalf("expected stable internal error, got %+v", result.Errors)
	}
	if strings.Contains(result.Errors[0].Message, "duckdb") || strings.Contains(result.Errors[0].Message, "private") {
		t.Fatalf("GraphQL exposed resolver details: %+v", result.Errors)
	}
}

func TestGraphQLRejectsReaderIngestMutations(t *testing.T) {
	ctx := middleware.WithAuthResult(context.Background(), &middleware.AuthResult{
		Authorized: true,
		Role:       "reader",
	})
	result := testGraphQLServer(t).executeQuery(
		ctx,
		`mutation { ingestEvent(event: {id: "evt", service: "api", kind: "log"}) { status } }`,
		nil,
		"",
	)
	if len(result.Errors) == 0 || !strings.Contains(result.Errors[0].Message, "writer role required") {
		t.Fatalf("expected writer authorization error, got %+v", result.Errors)
	}
}

// remediationIncidentStore and remediationMatcher are focused fakes that record
// which signature the suggestion path scopes its similar-search to.
type remediationIncidentStore struct {
	incidents map[string]*models.Incident
}

func (*remediationIncidentStore) Save(context.Context, *models.Incident) error { return nil }
func (s *remediationIncidentStore) Get(_ context.Context, id string) (*models.Incident, error) {
	incident := s.incidents[id]
	if incident == nil {
		return nil, storage.ErrNotFound
	}
	return incident, nil
}
func (*remediationIncidentStore) GetBySignature(context.Context, string) (*models.Incident, error) {
	return nil, storage.ErrNotFound
}
func (*remediationIncidentStore) List(context.Context, int, int) ([]*models.Incident, error) {
	return nil, nil
}

type remediationMatcher struct {
	queriedSignatures []string
	similar           map[string][]*models.SimilarIncident
}

func (*remediationMatcher) Score(*models.IncidentSignature, *models.IncidentSignature) (float64, error) {
	return 0, nil
}
func (*remediationMatcher) TopK(*models.IncidentSignature, []*models.IncidentSignature, int) ([]matcher.Match, error) {
	return nil, nil
}
func (*remediationMatcher) ExtractSignature(context.Context, *models.IncidentContext) (*models.IncidentSignature, error) {
	return nil, nil
}
func (m *remediationMatcher) FindSimilar(_ context.Context, sig *models.IncidentSignature, _ int) ([]*models.SimilarIncident, error) {
	m.queriedSignatures = append(m.queriedSignatures, sig.SignatureID)
	return m.similar[sig.SignatureID], nil
}
func (*remediationMatcher) RegisterSignature(context.Context, *models.IncidentSignature) error {
	return nil
}
func (*remediationMatcher) UpdateSignature(context.Context, string, *models.SignatureUpdate) error {
	return nil
}

func newRemediationTestServer(t *testing.T) (*GraphQLServer, *remediationMatcher) {
	t.Helper()
	signatures := &graphQLSignatureStore{signatures: map[string]*models.IncidentSignature{
		"sig-1": {
			SignatureID: "sig-1", Shape: "first",
			Symptoms:    []models.SymptomType{models.SymptomTypeLatencySpike},
			Remediation: []string{"restart-cache"},
		},
		"sig-2": {
			SignatureID: "sig-2", Shape: "second",
			Symptoms:    []models.SymptomType{models.SymptomTypeErrorRate},
			Remediation: []string{"rollback-deploy"},
		},
	}}
	match := &remediationMatcher{similar: map[string][]*models.SimilarIncident{
		"sig-1": {{IncidentID: "sig-2", Similarity: 0.9, Resolution: "rollback-deploy"}},
		"sig-2": {{IncidentID: "sig-1", Similarity: 0.7, Resolution: "restart-cache"}},
	}}
	server := &GraphQLServer{
		incidents: &remediationIncidentStore{incidents: map[string]*models.Incident{
			"inc-1": {ID: "inc-1", SignatureID: "sig-1", Status: "active", Severity: "high", PrimaryService: "api"},
			"inc-2": {ID: "inc-2", SignatureID: "sig-2", Status: "active", Severity: "high", PrimaryService: "api"},
		}},
		signatures: signatures,
		match:      match,
	}
	if err := server.initSchema(); err != nil {
		t.Fatal(err)
	}
	return server, match
}

func TestRemediationSuggestionsScopeSearchToIncidentSignature(t *testing.T) {
	server, match := newRemediationTestServer(t)

	suggestions, err := server.handleRemediationSuggestions(context.Background(), map[string]interface{}{
		"incidentId": "inc-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The similar-search must use only the queried incident's signature, never a
	// global signature listing.
	if len(match.queriedSignatures) != 1 || match.queriedSignatures[0] != "sig-1" {
		t.Fatalf("FindSimilar scoped to %v, want [sig-1]", match.queriedSignatures)
	}

	got, ok := suggestions.([]map[string]interface{})
	if !ok || len(got) != 1 {
		t.Fatalf("unexpected suggestions: %#v", suggestions)
	}
	if action := got[0]["action"]; action != "rollback-deploy" {
		t.Fatalf("action = %v, want the matched signature's remediation %q (not a symptom)", action, "rollback-deploy")
	}
	sig, ok := got[0]["signature"].(*models.IncidentSignature)
	if !ok || sig.SignatureID != "sig-1" {
		t.Fatalf("suggestion signature = %#v, want sig-1", got[0]["signature"])
	}
}

func TestRemediationSuggestionsAreIncidentSpecific(t *testing.T) {
	server, _ := newRemediationTestServer(t)

	first, err := server.handleRemediationSuggestions(context.Background(), map[string]interface{}{"incidentId": "inc-1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.handleRemediationSuggestions(context.Background(), map[string]interface{}{"incidentId": "inc-2"})
	if err != nil {
		t.Fatal(err)
	}
	firstAction := first.([]map[string]interface{})[0]["action"]
	secondAction := second.([]map[string]interface{})[0]["action"]
	if firstAction == secondAction {
		t.Fatalf("distinct incidents returned identical action %q", firstAction)
	}
	if firstAction != "rollback-deploy" || secondAction != "restart-cache" {
		t.Fatalf("incident-specific actions = (%v, %v), want (rollback-deploy, restart-cache)", firstAction, secondAction)
	}
}

func TestGraphQLRemediationSuggestionsReturnIncidentSpecificAction(t *testing.T) {
	server, _ := newRemediationTestServer(t)
	result := server.executeQuery(
		context.Background(),
		`query { remediationSuggestions(incidentId: "inc-1") { action confidence signature { signature_id } } }`,
		nil,
		"",
	)
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected GraphQL errors: %+v", result.Errors)
	}
	data := result.Data.(map[string]interface{})
	suggestions := data["remediationSuggestions"].([]interface{})
	if len(suggestions) != 1 {
		t.Fatalf("unexpected suggestions: %#v", suggestions)
	}
	suggestion := suggestions[0].(map[string]interface{})
	if suggestion["action"] != "rollback-deploy" {
		t.Fatalf("action = %v, want rollback-deploy", suggestion["action"])
	}
	signature := suggestion["signature"].(map[string]interface{})
	if signature["signature_id"] != "sig-1" {
		t.Fatalf("signature_id = %v, want sig-1", signature["signature_id"])
	}
}

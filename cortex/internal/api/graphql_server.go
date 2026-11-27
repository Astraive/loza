package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/astraive/loza/cortex/internal/config"
	"github.com/astraive/loza/cortex/internal/graph"
	"github.com/astraive/loza/cortex/internal/learner"
	"github.com/astraive/loza/cortex/internal/matcher"
	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/processor"
	"github.com/astraive/loza/cortex/internal/reconstructor"
	"github.com/astraive/loza/cortex/internal/storage"
	"github.com/astraive/loza/cortex/internal/topology"
	transportcontracts "github.com/astraive/loza/spec/transport/contracts"
	"github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/gqlerrors"
	"github.com/rs/zerolog/log"
)

const (
	maxGraphQLDepth     = 10
	maxSimilarIncidents = 10
)

// Introspection fields are disabled on the externally exposed schema.

type GraphQLServer struct {
	config      *config.Config
	processor   *processor.EventProcessor
	topology    *topology.Resolver
	graph       *graph.Builder
	match       matcher.SignatureService
	remediation *learner.Learner
	recon       *reconstructor.IncidentReconstructor
	incidents   storage.IncidentStore
	signatures  storage.SignatureStore
	schema      graphql.Schema
	schemaErr   error
}

type graphQLClientError struct {
	message string
}

func (e *graphQLClientError) Error() string {
	return e.message
}

func newGraphQLClientError(message string) error {
	return &graphQLClientError{message: message}
}

func publicGraphQLResolverError(err error) (error, bool) {
	var clientError *graphQLClientError
	switch {
	case errors.As(err, &clientError):
		return clientError, true
	case errors.Is(err, storage.ErrNotFound):
		return newGraphQLClientError("not found"), true
	case errors.Is(err, processor.ErrInvalidEvent):
		return newGraphQLClientError("invalid event"), true
	default:
		return errors.New("internal error"), false
	}
}

func NewGraphQLServer(cfg *config.Config, stor storage.Storage) *GraphQLServer {
	return newGraphQLServer(cfg, stor, graph.NewVersionedGraphStore(stor.Graph()))
}

func newGraphQLServer(cfg *config.Config, stor storage.Storage, graphStore *graph.VersionedGraphStore) *GraphQLServer {
	topology := topology.NewResolver(stor.Topology())
	graphBuilder := graph.NewBuilder(graphStore)
	matching, err := matcher.NewConfiguredSignatureMatcher(stor.Signatures(), cfg.Matcher)
	if err != nil {
		matching = matcher.NewSignatureMatcher(stor.Signatures())
	}
	remediation := learner.NewLearner(stor.Remediations(), stor.Feedback(), stor.Signatures())
	recon := reconstructor.NewIncidentReconstructor(graphBuilder, matching, remediation, stor.Incidents())
	eventProc := processor.NewConfiguredEventProcessor(stor.Events(), stor.Topology(), graphStore, cfg.PIIRedaction)

	server := &GraphQLServer{
		config:      cfg,
		processor:   eventProc,
		topology:    topology,
		graph:       graphBuilder,
		match:       matching,
		remediation: remediation,
		recon:       recon,
		incidents:   stor.Incidents(),
		signatures:  stor.Signatures(),
	}
	server.schemaErr = server.initSchema()
	return server
}

func (s *GraphQLServer) Handler() http.Handler {
	return http.HandlerFunc(s.handleGraphQL)
}

func (s *GraphQLServer) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req transportcontracts.GraphQLRequest
	if r.Method == "GET" {
		req.Query = r.URL.Query().Get("query")
		req.OperationName = r.URL.Query().Get("operationName")
	} else {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "Failed to read request body")
			return
		}
		defer r.Body.Close()

		if err := json.Unmarshal(body, &req); err != nil {
			s.writeError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}
	}

	if isIntrospectionQuery(req.Query) {
		s.writeError(w, http.StatusForbidden, "introspection is not allowed")
		return
	}

	// Analyse the parsed query before executing it. Counting literal braces in
	// the request text misses alias fan-out and repeated fragment spreads, so
	// enforce both effective depth and total field complexity on the AST.
	cost, err := analyzeGraphQLQuery(req.Query, req.OperationName)
	if err == nil {
		if cost.depth > maxGraphQLDepth {
			s.writeError(w, http.StatusBadRequest, "query exceeds maximum depth")
			return
		}
		if cost.complexity > maxGraphQLComplexity {
			s.writeError(w, http.StatusBadRequest, "query exceeds maximum complexity")
			return
		}
	}

	// Check query depth before executing (the WebSocket path applies the same
	// validation inside executeQuery).
	if err := validateGraphQLQueryDepth(req.Query); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.schemaErr != nil {
		s.writeError(w, http.StatusInternalServerError, "GraphQL schema unavailable")
		return
	}
	result := s.executeQuery(r.Context(), req.Query, req.Variables, req.OperationName)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Error().Err(err).Msg("failed to encode graphql response")
	}
}

func (s *GraphQLServer) writeError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	response := transportcontracts.GraphQLResponse{
		Errors: []transportcontracts.GraphQLError{{Message: message}},
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("failed to encode error response")
	}
}

// validateGraphQLQueryDepth enforces the query nesting limit for every
// GraphQL entry point. Both handleGraphQL (HTTP) and executeQuery (HTTP and
// GraphQL-over-WebSocket) call it so no path can bypass the check.
func validateGraphQLQueryDepth(query string) error {
	if queryDepth(query) > maxGraphQLDepth {
		return newGraphQLClientError("query exceeds maximum depth")
	}
	return nil
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

// isIntrospectionQuery blocks the reserved GraphQL introspection fields.
func isIntrospectionQuery(query string) bool {
	return containsWord(query, "__schema") ||
		containsWord(query, "__type") ||
		containsWord(query, "__typename")
}

func (s *GraphQLServer) executeQuery(ctx context.Context, query string, vars map[string]interface{}, operationName string) *graphql.Result {
	if isIntrospectionQuery(query) {
		return &graphql.Result{Errors: []gqlerrors.FormattedError{{Message: "introspection is not allowed"}}}
	}
	// Enforce the shared query-depth limit here as well, so the
	// GraphQL-over-WebSocket path cannot bypass the HTTP-level check.
	if err := validateGraphQLQueryDepth(query); err != nil {
		return &graphql.Result{Errors: []gqlerrors.FormattedError{{Message: err.Error()}}}
	}
	return graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  query,
		VariableValues: vars,
		OperationName:  operationName,
		Context:        ctx,
	})
}

func containsWord(s, word string) bool {
	if word == "" || len(word) > len(s) {
		return false
	}
	for i := 0; i+len(word) <= len(s); i++ {
		if s[i:i+len(word)] != word {
			continue
		}
		if i > 0 && isAlpha(rune(s[i-1])) {
			continue
		}
		end := i + len(word)
		if end < len(s) && isAlpha(rune(s[end])) {
			continue
		}
		return true
	}
	return false
}

func isAlpha(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}

func normalizeGraphQLValue(value interface{}) (interface{}, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode GraphQL result: %w", err)
	}
	var normalized interface{}
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, fmt.Errorf("normalize GraphQL result: %w", err)
	}
	return normalized, nil
}

func graphQLObject(name string, fields map[string]graphql.Output) *graphql.Object {
	configured := make(graphql.Fields, len(fields))
	for fieldName, fieldType := range fields {
		configured[fieldName] = &graphql.Field{Type: fieldType}
	}
	return graphql.NewObject(graphql.ObjectConfig{Name: name, Fields: configured})
}

func (s *GraphQLServer) resolver(handler func(context.Context, map[string]interface{}) (interface{}, error), writerOnly bool) graphql.FieldResolveFn {
	return func(params graphql.ResolveParams) (interface{}, error) {
		if writerOnly && !hasWriterRole(params.Context) {
			return nil, newGraphQLClientError("writer role required for ingest operations")
		}
		value, err := handler(params.Context, params.Args)
		if err == nil {
			value, err = normalizeGraphQLValue(value)
		}
		if err == nil {
			return value, nil
		}
		publicError, safe := publicGraphQLResolverError(err)
		if !safe {
			log.Error().
				Err(err).
				Str("event.name", "graphql.resolver").
				Str("event.kind", "request").
				Str("event.outcome", "error").
				Str("graphql.field", params.Info.FieldName).
				Msg("graphql resolver failed")
		}
		return nil, publicError
	}
}

func (s *GraphQLServer) initSchema() error {
	stringList := graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(graphql.String)))
	nodeType := graphQLObject("Node", map[string]graphql.Output{
		"id": graphql.NewNonNull(graphql.ID), "type": graphql.NewNonNull(graphql.String), "label": graphql.NewNonNull(graphql.String),
	})
	edgeType := graphQLObject("Edge", map[string]graphql.Output{
		"id": graphql.NewNonNull(graphql.ID), "from_node_id": graphql.NewNonNull(graphql.ID),
		"to_node_id": graphql.NewNonNull(graphql.ID), "type": graphql.NewNonNull(graphql.String),
		"weight": graphql.NewNonNull(graphql.Float),
	})
	graphViewType := graphQLObject("GraphView", map[string]graphql.Output{
		"nodes": graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(nodeType))),
		"edges": graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(edgeType))),
	})
	incidentType := graphQLObject("Incident", map[string]graphql.Output{
		"id": graphql.NewNonNull(graphql.ID), "timestamp": graphql.NewNonNull(graphql.String),
		"signature_id": graphql.String, "status": graphql.NewNonNull(graphql.String),
		"severity": graphql.NewNonNull(graphql.String), "primary_service": graphql.NewNonNull(graphql.String),
		"affected_services": stringList, "resolved_at": graphql.String,
	})
	causalEventType := graphQLObject("CausalEvent", map[string]graphql.Output{
		"event_id": graphql.NewNonNull(graphql.ID), "timestamp": graphql.NewNonNull(graphql.String),
		"kind": graphql.NewNonNull(graphql.String), "service": graphql.NewNonNull(graphql.String),
		"description": graphql.NewNonNull(graphql.String), "causal_edge": graphql.NewNonNull(graphql.String),
		"signal_density": graphql.NewNonNull(graphql.Float),
	})
	symptomType := graphQLObject("Symptom", map[string]graphql.Output{
		"type": graphql.NewNonNull(graphql.String), "service": graphql.NewNonNull(graphql.String),
		"metric": graphql.String, "threshold": graphql.Float, "observed": graphql.Float,
		"description": graphql.NewNonNull(graphql.String),
	})
	remediationType := graphQLObject("RemediationAction", map[string]graphql.Output{
		"action": graphql.NewNonNull(graphql.String), "description": graphql.NewNonNull(graphql.String),
		"success_rate": graphql.NewNonNull(graphql.Float), "avg_time_to_resolve_seconds": graphql.NewNonNull(graphql.Int),
		"priority": graphql.NewNonNull(graphql.Int),
	})
	similarIncidentType := graphQLObject("SimilarIncident", map[string]graphql.Output{
		"incident_id": graphql.NewNonNull(graphql.ID), "timestamp": graphql.NewNonNull(graphql.String),
		"similarity": graphql.NewNonNull(graphql.Float), "shape": graphql.NewNonNull(graphql.String),
		"resolution": graphql.NewNonNull(graphql.String), "resolution_time_seconds": graphql.NewNonNull(graphql.Int),
		"success_rate": graphql.NewNonNull(graphql.Float),
	})
	incidentContextType := graphQLObject("IncidentContext", map[string]graphql.Output{
		"incident_id": graphql.NewNonNull(graphql.ID), "timestamp": graphql.NewNonNull(graphql.String),
		"causal_chain":      graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(causalEventType))),
		"related_services":  stringList,
		"similar_incidents": graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(similarIncidentType))),
		"symptoms":          graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(symptomType))),
		"suggested_actions": graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(remediationType))),
		"confidence":        graphql.NewNonNull(graphql.Float), "explain": graphql.NewNonNull(graphql.String),
	})
	signatureType := graphQLObject("Signature", map[string]graphql.Output{
		"signature_id": graphql.NewNonNull(graphql.ID), "shape": graphql.NewNonNull(graphql.String),
		"service_roles": stringList, "symptoms": stringList,
		"occurrence_count":            graphql.NewNonNull(graphql.Int),
		"avg_resolution_time_seconds": graphql.NewNonNull(graphql.Int),
		"behavioral_hash":             graphql.NewNonNull(graphql.String), "created_at": graphql.NewNonNull(graphql.String),
	})
	actionResultType := graphQLObject("ActionResult", map[string]graphql.Output{
		"status": graphql.NewNonNull(graphql.String),
	})
	batchResultType := graphQLObject("BatchResult", map[string]graphql.Output{
		"status": graphql.NewNonNull(graphql.String), "count": graphql.NewNonNull(graphql.Int),
	})
	suggestionType := graphQLObject("Suggestion", map[string]graphql.Output{
		"action": graphql.NewNonNull(graphql.String), "confidence": graphql.NewNonNull(graphql.Float),
		"signature": graphql.NewNonNull(signatureType),
	})
	eventInput := graphql.NewInputObject(graphql.InputObjectConfig{
		Name: "EventInput",
		Fields: graphql.InputObjectConfigFieldMap{
			"id":         &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.ID)},
			"timestamp":  &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
			"service":    &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
			"kind":       &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)},
			"provenance": &graphql.InputObjectFieldConfig{Type: graphql.String},
		},
	})

	query := graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: graphql.Fields{
		"reconstruct": &graphql.Field{
			Type: graphql.NewNonNull(incidentContextType),
			Args: graphql.FieldConfigArgument{
				"incidentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				"mode":       &graphql.ArgumentConfig{Type: graphql.String},
			},
			Resolve: s.resolver(s.handleReconstruct, false),
		},
		"incident": &graphql.Field{
			Type:    incidentType,
			Args:    graphql.FieldConfigArgument{"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)}},
			Resolve: s.resolver(s.handleIncident, false),
		},
		"serviceGraph": &graphql.Field{
			Type: graphql.NewNonNull(graphViewType),
			Args: graphql.FieldConfigArgument{
				"service": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				"depth":   &graphql.ArgumentConfig{Type: graphql.Int},
			},
			Resolve: s.resolver(s.handleServiceGraph, false),
		},
		"incidentGraph": &graphql.Field{
			Type: graphql.NewNonNull(graphViewType),
			Args: graphql.FieldConfigArgument{
				"incidentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)},
				"depth":      &graphql.ArgumentConfig{Type: graphql.Int},
			},
			Resolve: s.resolver(s.handleIncidentGraph, false),
		},
		"remediationSuggestions": &graphql.Field{
			Type:    graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(suggestionType))),
			Args:    graphql.FieldConfigArgument{"incidentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)}},
			Resolve: s.resolver(s.handleRemediationSuggestions, false),
		},
		"similarIncidents": &graphql.Field{
			Type:    graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(incidentType))),
			Args:    graphql.FieldConfigArgument{"incidentId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)}},
			Resolve: s.resolver(s.handleSimilarIncidents, false),
		},
		"signature": &graphql.Field{
			Type:    signatureType,
			Args:    graphql.FieldConfigArgument{"id": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.ID)}},
			Resolve: s.resolver(s.handleSignature, false),
		},
		"signatures": &graphql.Field{
			Type:    graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(signatureType))),
			Resolve: s.resolver(s.handleSignatures, false),
		},
	}})
	mutation := graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: graphql.Fields{
		"ingestEvent": &graphql.Field{
			Type:    graphql.NewNonNull(actionResultType),
			Args:    graphql.FieldConfigArgument{"event": &graphql.ArgumentConfig{Type: graphql.NewNonNull(eventInput)}},
			Resolve: s.resolver(s.handleIngestEvent, true),
		},
		"ingestBatch": &graphql.Field{
			Type: graphql.NewNonNull(batchResultType),
			Args: graphql.FieldConfigArgument{
				"events": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(eventInput)))},
			},
			Resolve: s.resolver(s.handleIngestBatch, true),
		},
	}})

	compiled, err := graphql.NewSchema(graphql.SchemaConfig{Query: query, Mutation: mutation})
	if err != nil {
		return fmt.Errorf("build GraphQL schema: %w", err)
	}
	s.schema = compiled
	return nil
}

func graphQLEventFromInput(eventMap map[string]interface{}) (*models.Event, error) {
	timestampText, ok := eventMap["timestamp"].(string)
	if !ok || timestampText == "" {
		return nil, newGraphQLClientError("event timestamp is required")
	}
	timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
	if err != nil {
		return nil, newGraphQLClientError("event timestamp must be RFC3339")
	}

	event := &models.Event{Timestamp: timestamp}
	if id, ok := eventMap["id"].(string); ok {
		event.ID = id
	}
	if service, ok := eventMap["service"].(string); ok {
		event.Service = service
	}
	if kind, ok := eventMap["kind"].(string); ok {
		event.Kind = models.EventKind(kind)
	}
	if provenance, ok := eventMap["provenance"].(string); ok {
		event.Provenance = provenance
	}

	event.Raw = make(map[string]interface{}, len(eventMap))
	for key, value := range eventMap {
		switch key {
		case "id", "timestamp", "service", "kind", "provenance":
			continue
		default:
			event.Raw[key] = value
		}
	}
	return event, nil
}

func (s *GraphQLServer) handleIngestEvent(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	eventMap, ok := vars["event"].(map[string]interface{})
	if !ok {
		return nil, newGraphQLClientError("event variables required")
	}

	event, err := graphQLEventFromInput(eventMap)
	if err != nil {
		return nil, err
	}

	if err := s.processor.ProcessEvent(ctx, event); err != nil {
		return nil, err
	}

	return map[string]interface{}{"status": "accepted"}, nil
}

func (s *GraphQLServer) handleIngestBatch(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	eventsRaw, ok := vars["events"].([]interface{})
	if !ok {
		return nil, newGraphQLClientError("events array required")
	}

	events := make([]*models.Event, 0, len(eventsRaw))
	for _, e := range eventsRaw {
		eventMap, ok := e.(map[string]interface{})
		if !ok {
			continue
		}

		event, err := graphQLEventFromInput(eventMap)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}

	if err := s.processor.ProcessBatch(ctx, events); err != nil {
		return nil, err
	}

	return map[string]interface{}{"status": "accepted", "count": len(events)}, nil
}

func (s *GraphQLServer) handleReconstruct(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	incidentID, ok := vars["incidentId"].(string)
	if !ok {
		return nil, newGraphQLClientError("incidentId required")
	}

	mode := "fast"
	if m, ok := vars["mode"].(string); ok {
		mode = m
	}

	var recon *models.IncidentContext
	var err error
	if mode == "deep" {
		recon, err = s.recon.ReconstructDeep(ctx, incidentID)
	} else {
		recon, err = s.recon.ReconstructFast(ctx, incidentID)
	}

	if err != nil {
		return nil, err
	}

	return recon, nil
}

func (s *GraphQLServer) handleIncident(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	incidentID, ok := vars["id"].(string)
	if !ok {
		return nil, newGraphQLClientError("id required")
	}

	incident, err := s.incidents.Get(ctx, incidentID)
	if err != nil {
		return nil, err
	}

	return incident, nil
}

func (s *GraphQLServer) handleServiceGraph(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	service, ok := vars["service"].(string)
	if !ok {
		return nil, newGraphQLClientError("service required")
	}

	depth := defaultGraphDepth
	if d, ok := vars["depth"].(int); ok {
		depth = clampedGraphDepth(depthFromContext(ctx), d)
	}

	graphView, err := s.graph.GetServiceGraph(ctx, service, depth)
	if err != nil {
		return nil, err
	}

	return graphView, nil
}

func (s *GraphQLServer) handleIncidentGraph(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	incidentID, ok := vars["incidentId"].(string)
	if !ok {
		return nil, newGraphQLClientError("incidentId required")
	}

	depth := defaultGraphDepth
	if d, ok := vars["depth"].(int); ok {
		depth = clampedGraphDepth(depthFromContext(ctx), d)
	}

	graphView, err := s.graph.GetIncidentGraph(ctx, incidentID, depth)
	if err != nil {
		return nil, err
	}

	return graphView, nil
}

func (s *GraphQLServer) handleRemediationSuggestions(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	incidentID, ok := vars["incidentId"].(string)
	if !ok {
		return nil, newGraphQLClientError("incidentId required")
	}

	incident, err := s.incidents.Get(ctx, incidentID)
	if err != nil {
		return nil, err
	}

	// Scope remediation to the signature of the incident being queried. Without a
	// signature there is no incident-specific remediation to suggest.
	if incident.SignatureID == "" {
		return []map[string]interface{}{}, nil
	}

	incidentSignature, err := s.signatures.Get(ctx, incident.SignatureID)
	if err != nil {
		return nil, err
	}

	similar, err := s.match.FindSimilar(ctx, incidentSignature, 5)
	if err != nil {
		return nil, err
	}

	suggestions := make([]map[string]interface{}, 0, len(similar))
	for _, sim := range similar {
		// SimilarIncident.Resolution is the remediation recorded on the matched
		// signature. Fall back to the queried incident's own remediation when the
		// match does not carry one.
		action := sim.Resolution
		if action == "" && len(incidentSignature.Remediation) > 0 {
			action = incidentSignature.Remediation[0]
		}
		if action == "" {
			continue
		}
		suggestions = append(suggestions, map[string]interface{}{
			"action":     action,
			"confidence": sim.Similarity,
			"signature":  incidentSignature,
		})
	}

	return suggestions, nil
}

func (s *GraphQLServer) handleSimilarIncidents(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	incidentID, ok := vars["incidentId"].(string)
	if !ok {
		return nil, newGraphQLClientError("incidentId required")
	}

	target, err := s.incidents.Get(ctx, incidentID)
	if err != nil {
		return nil, err
	}

	// Similarity is computed from the target incident's signature. Without one
	// there is nothing to compare against, so there are no similar incidents.
	if target.SignatureID == "" {
		return []*models.Incident{}, nil
	}
	targetSig, err := s.signatures.Get(ctx, target.SignatureID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return []*models.Incident{}, nil
		}
		return nil, err
	}

	matches, err := s.match.FindSimilar(ctx, targetSig, maxSimilarIncidents)
	if err != nil {
		return nil, err
	}

	// FindSimilar may return matches in an arbitrary order (notably from the
	// storage fallback), so rank them by score before resolving to incidents.
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Similarity > matches[j].Similarity
	})

	similar := make([]*models.Incident, 0, len(matches))
	seen := map[string]bool{incidentID: true}
	for _, candidate := range matches {
		// SimilarIncident.IncidentID carries the matched signature ID.
		inc, err := s.incidents.GetBySignature(ctx, candidate.IncidentID)
		if err != nil || inc == nil || seen[inc.ID] {
			continue
		}
		seen[inc.ID] = true
		similar = append(similar, inc)
	}

	return similar, nil
}

func (s *GraphQLServer) handleSignature(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	sigID, ok := vars["id"].(string)
	if !ok {
		return nil, newGraphQLClientError("id required")
	}

	signature, err := s.signatures.Get(ctx, sigID)
	if err != nil {
		return nil, err
	}

	return signature, nil
}

func (s *GraphQLServer) handleSignatures(ctx context.Context, vars map[string]interface{}) (interface{}, error) {
	sigs, err := s.signatures.List(ctx, 50)
	if err != nil {
		return nil, err
	}

	return sigs, nil
}

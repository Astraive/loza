package models

import (
	"time"
)

type CausalEvent struct {
	EventID       string                 `json:"event_id"`
	Timestamp     time.Time              `json:"timestamp"`
	Kind          string                 `json:"kind"`
	Service       string                 `json:"service"`
	Description   string                 `json:"description"`
	Attributes    map[string]interface{} `json:"attributes"`
	CausalEdge    string                 `json:"causal_edge"`
	SignalDensity float64                `json:"signal_density"`
}

type RemediationAction struct {
	Action           string  `json:"action"`
	Description      string  `json:"description"`
	SuccessRate      float64 `json:"success_rate"`
	AvgTimeToResolve int64   `json:"avg_time_to_resolve_seconds"`
	Priority         int     `json:"priority"`
}

type IncidentContext struct {
	IncidentID       string              `json:"incident_id"`
	Timestamp        time.Time           `json:"timestamp"`
	CausalChain      []*CausalEvent      `json:"causal_chain"`
	RelatedServices  []string            `json:"related_services"`
	SimilarIncidents []*SimilarIncident  `json:"similar_incidents"`
	Symptoms         []Symptom           `json:"symptoms"`
	SuggestedActions []RemediationAction `json:"suggested_actions"`

	// Partial is true when one or more enrichment subsystems (signature
	// extraction, similarity search or remediation lookup) failed. It lets
	// callers distinguish a genuine low-signal reconstruction from one whose
	// matcher/learner backend failed.
	Partial bool `json:"partial,omitempty"`
	// Warnings holds a human-readable diagnostic for every failed subsystem.
	Warnings []string `json:"warnings,omitempty"`

	Confidence    float64        `json:"confidence"`
	Explain       string         `json:"explain"`
	ExplainReport *ExplainReport `json:"explain_report,omitempty"`
}

type ExplainReport struct {
	Narrative             string              `json:"narrative"`
	ConfidenceBreakdown   ConfidenceBreakdown `json:"confidence_breakdown"`
	KeyFindings           []string            `json:"key_findings"`
	DataGaps              []string            `json:"data_gaps"`
	AlternativeHypotheses []string            `json:"alternative_hypotheses"`
}

type ConfidenceBreakdown struct {
	CausalChainStrength   float64 `json:"causal_chain_strength"`
	SymptomCoverage       float64 `json:"symptom_coverage"`
	HistoricalMatch       float64 `json:"historical_match"`
	RemediationConfidence float64 `json:"remediation_confidence"`
}

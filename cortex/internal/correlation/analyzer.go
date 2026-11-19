package correlation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
	"github.com/rs/zerolog/log"
)

// Config holds correlation analysis parameters.
type Config struct {
	Enabled                   bool          `yaml:"enabled"`
	AnalysisInterval          time.Duration `yaml:"analysis_interval"`
	CoOccurrenceWindow        time.Duration `yaml:"co_occurrence_window"`
	DeploymentAdjacencyWindow time.Duration `yaml:"deployment_adjacency_window"`
	MinCoOccurrenceCount      int           `yaml:"min_co_occurrence_count"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:                   true,
		AnalysisInterval:          time.Minute,
		CoOccurrenceWindow:        5 * time.Minute,
		DeploymentAdjacencyWindow: 30 * time.Minute,
		MinCoOccurrenceCount:      3,
	}
}

// FromConfig creates a Config from the cortex config file values.
func FromConfig(enabled bool, analysisInterval, coOccurrenceWindow, deploymentAdjacencyWindow time.Duration, minCoOccurrenceCount int) Config {
	return Config{
		Enabled:                   enabled,
		AnalysisInterval:          analysisInterval,
		CoOccurrenceWindow:        coOccurrenceWindow,
		DeploymentAdjacencyWindow: deploymentAdjacencyWindow,
		MinCoOccurrenceCount:      minCoOccurrenceCount,
	}
}

// Analyzer runs as a background goroutine that periodically scans recent events
// and synthesizes dynamic relationship edges.
type Analyzer struct {
	cfg        Config
	eventStore storage.EventStore
	graphStore storage.GraphStore
}

// NewAnalyzer creates a new CorrelationAnalyzer.
func NewAnalyzer(cfg Config, eventStore storage.EventStore, graphStore storage.GraphStore) *Analyzer {
	return &Analyzer{
		cfg:        cfg,
		eventStore: eventStore,
		graphStore: graphStore,
	}
}

// Run starts the background analysis loop. Blocks until ctx is cancelled.
func (a *Analyzer) Run(ctx context.Context) {
	if !a.cfg.Enabled {
		return
	}

	log.Info().Dur("interval", a.cfg.AnalysisInterval).Msg("Starting correlation analyzer")
	ticker := time.NewTicker(a.cfg.AnalysisInterval)
	defer ticker.Stop()

	// Run once on startup.
	a.runAnalysis(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Correlation analyzer stopped")
			return
		case <-ticker.C:
			a.runAnalysis(ctx)
		}
	}
}

func (a *Analyzer) runAnalysis(ctx context.Context) {
	startedAt := time.Now()
	err := a.analyze(ctx)
	event := log.Info().
		Str("event.name", "cortex.correlation.analysis").
		Str("event.kind", "job").
		Dur("duration", time.Since(startedAt))
	if err != nil {
		event = event.Err(err).Str("event.outcome", "error")
	} else {
		event = event.Str("event.outcome", "success")
	}
	event.Msg("Correlation analysis completed")
}

func (a *Analyzer) analyze(ctx context.Context) error {
	events, err := a.eventStore.List(ctx, 10000, 0)
	if err != nil {
		return fmt.Errorf("list events for correlation analysis: %w", err)
	}

	// Both synthesis passes consume the same event slice but operate on
	// different lookback windows. Filter with the larger of the two so that
	// deploy-to-anomaly pairs inside DeploymentAdjacencyWindow are not
	// discarded by the shorter co-occurrence window before they are examined.
	lookback := max(a.cfg.CoOccurrenceWindow*2, a.cfg.DeploymentAdjacencyWindow)
	cutoff := time.Now().Add(-lookback)
	var recent []*models.Event
	for _, e := range events {
		if e.Timestamp.After(cutoff) {
			recent = append(recent, e)
		}
	}
	if len(recent) == 0 {
		return nil
	}

	return errors.Join(
		a.synthesizeCoOccurrence(ctx, recent),
		a.synthesizeDeploymentAdjacency(ctx, recent),
	)
}

// synthesizeCoOccurrence creates co_occurred edges between events from different
// services within a sliding time window that share anomaly signals.
func (a *Analyzer) synthesizeCoOccurrence(ctx context.Context, events []*models.Event) error {
	window := a.cfg.CoOccurrenceWindow
	var persistenceErrors []error

	// Collect all anomalous events. A co-occurrence is decided by the actual
	// timestamps of the pair, not by a fixed 1-minute bucket: two signals that
	// fall within the sliding window must be correlated even if they sit on
	// opposite sides of a minute (or any other arbitrary bucket) boundary.
	var anomalies []*models.Event
	for _, event := range events {
		if isAnomaly(event) {
			anomalies = append(anomalies, event)
		}
	}

	// For each pair of anomalous events from different services, apply a real
	// overlap test against the configured window.
	seen := make(map[string]bool)
	for i, e1 := range anomalies {
		for _, e2 := range anomalies[i+1:] {
			if e1.Service == e2.Service {
				continue
			}

			timeDelta := math.Abs(e1.Timestamp.Sub(e2.Timestamp).Seconds())
			if timeDelta > window.Seconds() {
				continue
			}

			// Order endpoints by ID so the edge ID is deterministic regardless
			// of the order in which events were supplied.
			from, to := e1, e2
			if from.ID > to.ID {
				from, to = to, from
			}

			edgeID := fmt.Sprintf("co_%s_%s", from.ID, to.ID)
			if seen[edgeID] {
				continue
			}
			seen[edgeID] = true

			weight := 1.0 / (1.0 + timeDelta/window.Seconds())

			if err := a.saveEdge(ctx, &models.Edge{
				ID:         edgeID,
				FromNodeID: from.ID,
				ToNodeID:   to.ID,
				Type:       models.EdgeTypeCoOccurred,
				Weight:     weight,
				Attributes: map[string]interface{}{
					"time_delta_seconds": timeDelta,
					"window_seconds":     window.Seconds(),
				},
				CreatedAt: time.Now(),
			}); err != nil {
				persistenceErrors = append(persistenceErrors, err)
				if ctx.Err() != nil {
					return errors.Join(persistenceErrors...)
				}
			}
		}
	}
	return errors.Join(persistenceErrors...)
}

// synthesizeDeploymentAdjacency creates deployment_adjacent edges when a deployment
// on service A is followed by anomalous events on service B within 30 minutes.
func (a *Analyzer) synthesizeDeploymentAdjacency(ctx context.Context, events []*models.Event) error {
	window := a.cfg.DeploymentAdjacencyWindow
	var persistenceErrors []error
	now := time.Now()

	// Find deployment events
	var deploys []*models.Event
	for _, e := range events {
		if e.Kind == models.EventKindDeploy {
			deploys = append(deploys, e)
		}
	}

	// For each deploy, find anomalous events on other services within the window
	seen := make(map[string]bool)
	for _, deploy := range deploys {
		for _, event := range events {
			if event.Service == deploy.Service {
				continue
			}
			if event.Kind == models.EventKindDeploy {
				continue
			}
			if !isAnomaly(event) {
				continue
			}

			timeSinceDeploy := event.Timestamp.Sub(deploy.Timestamp)
			if timeSinceDeploy < 0 || timeSinceDeploy > window {
				continue
			}

			edgeID := fmt.Sprintf("dadj_%s_%s", deploy.ID, event.ID)
			if seen[edgeID] {
				continue
			}
			seen[edgeID] = true

			// Weight decays with time since deploy
			decay := 1.0 - float64(timeSinceDeploy)/float64(window)
			weight := math.Max(0.1, decay)

			if err := a.saveEdge(ctx, &models.Edge{
				ID:         edgeID,
				FromNodeID: deploy.ID,
				ToNodeID:   event.ID,
				Type:       models.EdgeTypeDeploymentAdjacent,
				Weight:     weight,
				Attributes: map[string]interface{}{
					"deploy_service":   deploy.Service,
					"affected_service": event.Service,
					"seconds_after":    int(timeSinceDeploy.Seconds()),
				},
				CreatedAt: now,
			}); err != nil {
				persistenceErrors = append(persistenceErrors, err)
				if ctx.Err() != nil {
					return errors.Join(persistenceErrors...)
				}
			}
		}
	}
	return errors.Join(persistenceErrors...)
}

func (a *Analyzer) saveEdge(ctx context.Context, edge *models.Edge) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if err = a.graphStore.SaveEdge(ctx, edge); err == nil {
			return nil
		}
		if attempt == 3 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(ctx.Err(), err)
		case <-timer.C:
		}
	}
	return fmt.Errorf("save correlation edge %q after 3 attempts: %w", edge.ID, err)
}

// isAnomaly returns true if an event represents an anomalous condition.
func isAnomaly(event *models.Event) bool {
	if event.Kind == models.EventKindIncidentSignal {
		return true
	}

	// Check for error-level logs
	if event.Kind == models.EventKindLog || event.Kind == models.EventKindLozaEvent {
		if level, ok := event.Raw["level"].(string); ok {
			if level == "error" || level == "fatal" {
				return true
			}
		}
	}

	// Check for anomalous metric values
	if event.Kind == models.EventKindMetric {
		if anomaly, ok := event.Raw["anomaly"].(bool); ok && anomaly {
			return true
		}
		// Check for threshold breaches
		if value, ok := event.Raw["value"].(float64); ok {
			if threshold, ok := event.Raw["threshold"].(float64); ok {
				if value > threshold {
					return true
				}
			}
		}
	}

	return false
}

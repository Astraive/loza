package storage

import "github.com/astraive/loza/cortex/internal/models"

// resolveLifecycleArrays returns the lifecycle primitive arrays that should be
// persisted for an event. A caller-supplied LifecycleData is authoritative when
// present, because Save/SaveBatch accept it precisely so the extracted
// lifecycle primitives are indexed. The arrays carried by the event itself are
// used as a fallback for callers that do not provide lifecycle data (or omit an
// individual array), preserving the previous behaviour for those callers.
func resolveLifecycleArrays(event *models.Event, lifecycle *LifecycleData) (
	checkpoints []*models.EventCheckpoint,
	processes []*models.EventProcess,
	groups []*models.EventGroup,
	timers []*models.EventTimer,
	links []*models.EventLink,
) {
	checkpoints = event.Checkpoints
	processes = event.Processes
	groups = event.Groups
	timers = event.Timers
	links = event.Links

	if lifecycle == nil {
		return
	}
	if lifecycle.Checkpoints != nil {
		checkpoints = lifecycle.Checkpoints
	}
	if lifecycle.Processes != nil {
		processes = lifecycle.Processes
	}
	if lifecycle.Groups != nil {
		groups = lifecycle.Groups
	}
	if lifecycle.Timers != nil {
		timers = lifecycle.Timers
	}
	if lifecycle.Links != nil {
		links = lifecycle.Links
	}
	return
}

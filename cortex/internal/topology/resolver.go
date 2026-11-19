package topology

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
	"github.com/astraive/loza/cortex/internal/storage"
)

// AliasCallback is called when a service alias is registered.
type AliasCallback func(ctx context.Context, from, to string, validFrom time.Time)

type Resolver struct {
	topologyStore storage.TopologyStore
	mu            sync.RWMutex
	callbacks     []AliasCallback
}

func NewResolver(store storage.TopologyStore) *Resolver {
	return &Resolver{topologyStore: store}
}

// aliasID builds a deterministic, unique ID for a service alias. The validity
// window is part of the identity: two aliases with the same from/to/validFrom
// but different validTo values are distinct records and must not share an ID
// (id is the primary key in the topology stores). A nil validTo is encoded
// explicitly so an open-ended alias cannot collide with a bounded one.
func aliasID(from, to string, validFrom time.Time, validTo *time.Time) string {
	if validTo != nil {
		return fmt.Sprintf("%s->%s-%d-%d", from, to, validFrom.Unix(), validTo.Unix())
	}
	return fmt.Sprintf("%s->%s-%d-open", from, to, validFrom.Unix())
}

// RegisterCallback adds a callback that fires when a new alias is registered.
func (r *Resolver) RegisterCallback(cb AliasCallback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbacks = append(r.callbacks, cb)
}

func (r *Resolver) ResolveService(ctx context.Context, alias string, timestamp time.Time) (string, error) {
	aliasObj, err := r.topologyStore.GetAlias(ctx, alias, timestamp.Format(time.RFC3339))
	if err != nil {
		// Only an explicit "no alias recorded" result is a successful identity
		// resolution. Genuine store failures must be surfaced so callers can
		// distinguish a missing alias from an outage instead of silently
		// routing on the unresolved name.
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, storage.ErrNotFound) {
			return alias, nil
		}
		return "", err
	}
	return aliasObj.Canonical, nil
}

func (r *Resolver) RegisterAlias(ctx context.Context, from string, to string, validFrom time.Time, validTo *time.Time) error {
	if validTo != nil && validFrom.After(*validTo) {
		return fmt.Errorf("valid_from must be before valid_to")
	}

	existingAliases, err := r.topologyStore.GetHistory(ctx, from)
	if err != nil {
		return fmt.Errorf("verify alias history for service %s: %w", from, err)
	}
	for _, existing := range existingAliases {
		if existing.Canonical == to {
			continue
		}
		if aliasPeriodsOverlap(existing.ValidFrom, existing.ValidTo, validFrom, validTo) {
			return fmt.Errorf("overlapping alias period for service %s", from)
		}
	}

	alias := &models.ServiceAlias{
		ID:        aliasID(from, to, validFrom, validTo),
		Alias:     from,
		Canonical: to,
		ValidFrom: validFrom,
		ValidTo:   validTo,
	}

	if err := r.topologyStore.SaveAlias(ctx, alias); err != nil {
		return err
	}

	// Snapshot the callbacks under the read lock so concurrent registrations
	// cannot race with iteration. Invoke them after releasing the lock because
	// a callback may itself register another callback.
	r.mu.RLock()
	callbacks := append([]AliasCallback(nil), r.callbacks...)
	r.mu.RUnlock()

	for _, cb := range callbacks {
		cb(ctx, from, to, validFrom)
	}

	return nil
}

// aliasPeriodsOverlap reports whether two alias validity periods intersect.
// A nil bound denotes an open-ended period (no end / no known start). Periods
// are treated as half-open intervals, so touching endpoints do not overlap.
func aliasPeriodsOverlap(fromA time.Time, toA *time.Time, fromB time.Time, toB *time.Time) bool {
	// a.from < b.to (an open-ended b has no upper bound)
	if toB != nil && !fromA.Before(*toB) {
		return false
	}
	// b.from < a.to (an open-ended a has no upper bound)
	if toA != nil && !fromB.Before(*toA) {
		return false
	}
	return true
}

func (r *Resolver) GetServiceHistory(ctx context.Context, service string) ([]*models.ServiceAlias, error) {
	aliases, err := r.topologyStore.GetHistory(ctx, service)
	if err != nil {
		return nil, err
	}

	if len(aliases) == 0 {
		return []*models.ServiceAlias{
			{
				Alias:     service,
				Canonical: service,
				ValidFrom: time.Time{},
			},
		}, nil
	}

	return aliases, nil
}

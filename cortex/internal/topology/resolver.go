package topology

import (
	"context"
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

// RegisterCallback adds a callback that fires when a new alias is registered.
func (r *Resolver) RegisterCallback(cb AliasCallback) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbacks = append(r.callbacks, cb)
}

func (r *Resolver) ResolveService(ctx context.Context, alias string, timestamp time.Time) (string, error) {
	aliasObj, err := r.topologyStore.GetAlias(ctx, alias, timestamp.Format(time.RFC3339))
	if err != nil {
		return alias, nil
	}
	return aliasObj.Canonical, nil
}

func (r *Resolver) RegisterAlias(ctx context.Context, from string, to string, validFrom time.Time, validTo *time.Time) error {
	if validTo != nil && validFrom.After(*validTo) {
		return fmt.Errorf("valid_from must be before valid_to")
	}

	existingAliases, err := r.topologyStore.GetHistory(ctx, from)
	if err == nil {
		for _, existing := range existingAliases {
			if existing.Canonical == to {
				continue
			}
			if aliasPeriodsOverlap(existing.ValidFrom, existing.ValidTo, validFrom, validTo) {
				return fmt.Errorf("overlapping alias period for service %s", from)
			}
		}
	}

	alias := &models.ServiceAlias{
		ID:        fmt.Sprintf("%s->%s-%d", from, to, validFrom.Unix()),
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

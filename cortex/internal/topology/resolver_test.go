package topology

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/astraive/loza/cortex/internal/models"
)

type fakeTopologyStore struct {
	aliasByKey map[string]*models.ServiceAlias
	history    map[string][]*models.ServiceAlias
	saved      []*models.ServiceAlias
}

func (f *fakeTopologyStore) SaveAlias(_ context.Context, alias *models.ServiceAlias) error {
	f.saved = append(f.saved, alias)
	return nil
}

func (f *fakeTopologyStore) GetAlias(_ context.Context, alias string, timestamp string) (*models.ServiceAlias, error) {
	if item, ok := f.aliasByKey[alias+"|"+timestamp]; ok {
		return item, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeTopologyStore) GetHistory(_ context.Context, service string) ([]*models.ServiceAlias, error) {
	if h, ok := f.history[service]; ok {
		return h, nil
	}
	return nil, nil
}

func TestResolveService(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &fakeTopologyStore{
		aliasByKey: map[string]*models.ServiceAlias{
			"svc-a|" + ts.Format(time.RFC3339): {Canonical: "svc-b"},
		},
	}
	resolver := NewResolver(store)

	got, err := resolver.ResolveService(context.Background(), "svc-a", ts)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "svc-b" {
		t.Fatalf("expected canonical service, got %s", got)
	}
}

func TestRegisterAliasAndHistory(t *testing.T) {
	store := &fakeTopologyStore{
		history: map[string][]*models.ServiceAlias{},
	}
	resolver := NewResolver(store)
	from := "svc-a"
	to := "svc-b"
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	if err := resolver.RegisterAlias(context.Background(), from, to, validFrom, nil); err != nil {
		t.Fatalf("register alias: %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("expected one saved alias, got %d", len(store.saved))
	}

	history, err := resolver.GetServiceHistory(context.Background(), "svc-a")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 1 || history[0].Alias != "svc-a" || history[0].Canonical != "svc-a" {
		t.Fatalf("unexpected history fallback: %+v", history)
	}
}

func TestGetServiceHistoryFallback(t *testing.T) {
	resolver := NewResolver(&fakeTopologyStore{history: map[string][]*models.ServiceAlias{}})
	got, err := resolver.GetServiceHistory(context.Background(), "svc-x")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 1 || got[0].Alias != "svc-x" || got[0].Canonical != "svc-x" {
		t.Fatalf("unexpected fallback history: %+v", got)
	}
}

func TestRegisterAliasRejectsOverlaps(t *testing.T) {
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	overlapTo := validFrom.Add(time.Hour)
	store := &fakeTopologyStore{
		history: map[string][]*models.ServiceAlias{
			"svc-a": {
				{Canonical: "svc-b", ValidTo: &overlapTo},
			},
		},
	}
	resolver := NewResolver(store)
	if err := resolver.RegisterAlias(context.Background(), "svc-a", "svc-c", validFrom, nil); err == nil {
		t.Fatal("expected overlap error")
	}
}

func TestCallbacksFireOnRegisterAlias(t *testing.T) {
	resolver := NewResolver(&fakeTopologyStore{history: map[string][]*models.ServiceAlias{}})

	var fired []string
	const callbackCount = 5
	for i := 0; i < callbackCount; i++ {
		resolver.RegisterCallback(func(_ context.Context, from, to string, _ time.Time) {
			fired = append(fired, from+"->"+to)
		})
	}

	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := resolver.RegisterAlias(context.Background(), "svc-a", "svc-b", validFrom, nil); err != nil {
		t.Fatalf("register alias: %v", err)
	}
	if len(fired) != callbackCount {
		t.Fatalf("expected %d callbacks to fire, got %d", callbackCount, len(fired))
	}
	for _, got := range fired {
		if got != "svc-a->svc-b" {
			t.Fatalf("unexpected callback arguments: %s", got)
		}
	}
}

func TestCallbackCanRegisterCallback(t *testing.T) {
	resolver := NewResolver(&fakeTopologyStore{history: map[string][]*models.ServiceAlias{}})

	fired := 0
	resolver.RegisterCallback(func(_ context.Context, _, _ string, _ time.Time) {
		fired++
		// A callback that registers another callback must not deadlock: the
		// callbacks slice must not be locked while they are invoked.
		resolver.RegisterCallback(func(context.Context, string, string, time.Time) {})
	})

	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := resolver.RegisterAlias(context.Background(), "svc-a", "svc-b", validFrom, nil); err != nil {
		t.Fatalf("register alias: %v", err)
	}
	if fired != 1 {
		t.Fatalf("expected one callback invocation, got %d", fired)
	}
}

func TestCallbacksConcurrentWithAliasRegistration(t *testing.T) {
	resolver := NewResolver(&fakeTopologyStore{history: map[string][]*models.ServiceAlias{}})
	validFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resolver.RegisterCallback(func(context.Context, string, string, time.Time) {})
		}()
	}

	// A single alias-registration goroutine iterates the callbacks slice while
	// the goroutines above append to it; under -race this is the reported race.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := resolver.RegisterAlias(context.Background(), "svc-a", "svc-b", validFrom, nil); err != nil {
				t.Errorf("register alias: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

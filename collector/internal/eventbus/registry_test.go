package eventbus

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// registryTestBus is a minimal Bus used only to exercise the registry.
type registryTestBus struct{}

func (registryTestBus) Publish(context.Context, string, []Envelope) error        { return nil }
func (registryTestBus) Subscribe(context.Context, string, string, Handler) error { return nil }
func (registryTestBus) Close(context.Context) error                              { return nil }
func (registryTestBus) Health(context.Context) Health                            { return Health{OK: true} }

func registryTestFactory(context.Context, Config) (Bus, error) {
	return registryTestBus{}, nil
}

// TestRegistryConcurrentRegisterAndNew exercises concurrent writes (Register)
// and reads (New) of the global factory registry. Without synchronization this
// is a data race that can panic with "concurrent map read and map write".
// Run with -race to detect the race directly.
func TestRegistryConcurrentRegisterAndNew(t *testing.T) {
	const (
		writers = 128
		readers = 128
	)

	ctx := context.Background()

	// Pre-register the type that readers will look up.
	const readType = "registry-concurrent-test"
	Register(readType, registryTestFactory)

	var wg sync.WaitGroup
	errCh := make(chan error, readers)

	// Concurrent writers registering distinct names.
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			Register(fmt.Sprintf("registry-concurrent-test-%d", i), registryTestFactory)
		}(i)
	}

	// Concurrent readers calling New against the registry.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus, err := New(ctx, Config{Type: readType})
			if err != nil {
				errCh <- err
				return
			}
			if bus == nil {
				errCh <- fmt.Errorf("New returned nil bus")
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("New during concurrent registration: %v", err)
	}
}

// TestRegistryReturnsRegisteredFactory verifies Register/New still behave
// correctly for the normal, non-concurrent path.
func TestRegistryReturnsRegisteredFactory(t *testing.T) {
	const typ = "registry-roundtrip-test"
	Register(typ, registryTestFactory)

	bus, err := New(context.Background(), Config{Type: typ})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := bus.(registryTestBus); !ok {
		t.Fatalf("New returned unexpected bus type %T", bus)
	}
}

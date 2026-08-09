package settings_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestRuntimeConvergesAcrossInvalidationLossReorderingAndMixedVersions(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	writeGeneration := func(value int64) settings.Record {
		t.Helper()
		record, err := settings.Set(t.Context(), durable, settings.Global(), key, value, settings.Change{
			Actor: "operator", Reason: "advance generation",
		})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	first := writeGeneration(1)
	provider := &countingFleetProvider{Provider: durable}
	source := newFleetInvalidationSource()
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		ReconnectDelay:       10 * time.Millisecond,
		InvalidationDebounce: 10 * time.Millisecond, Invalidations: source, WatchBuffer: 4,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	source.waitForSubscriptions(t, 1)
	assertRuntimeGeneration(t, runtime, key, 1)

	second := writeGeneration(2)
	source.send(t, settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           settings.Global(), Key: key.StableID(), Version: second.Version, State: second.State,
	})
	assertEventuallyGeneration(t, runtime, key, 2)
	calls := provider.Calls()
	source.send(t, settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           settings.Global(), Key: key.StableID(), Version: second.Version, State: second.State,
	})
	source.send(t, settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           settings.Global(), Key: key.StableID(), Version: first.Version, State: first.State,
	})
	time.Sleep(30 * time.Millisecond)
	if provider.Calls() != calls {
		t.Fatalf("duplicate or reordered invalidation refreshed: calls %d -> %d", calls, provider.Calls())
	}

	third := writeGeneration(3)
	source.send(t, settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion + 1,
		Scope:           settings.Global(), Key: key.StableID(), Version: third.Version, State: third.State,
	})
	assertEventuallyGeneration(t, runtime, key, 3)

	source.disconnect()
	source.waitForSubscriptions(t, 2)
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimePeriodicRefreshRepairsACompletelyLostInvalidation(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "periodic-generation", settings.IntCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "initial generation",
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		RefreshInterval: 20 * time.Millisecond,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(2), settings.Change{
		Actor: "operator", Reason: "lost invalidation",
	}); err != nil {
		t.Fatal(err)
	}
	assertEventuallyGeneration(t, runtime, key, 2)
}

func assertRuntimeGeneration(t *testing.T, runtime *settings.Runtime, key settings.Key[int64], want int64) {
	t.Helper()
	result, err := settings.ResolveCurrent(runtime, key)
	if err != nil || result.Value != want {
		t.Fatalf("generation = (%d, %v), want %d", result.Value, err, want)
	}
}

func assertEventuallyGeneration(t *testing.T, runtime *settings.Runtime, key settings.Key[int64], want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, err := settings.ResolveCurrent(runtime, key)
		if err == nil && result.Value == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	assertRuntimeGeneration(t, runtime, key, want)
}

type countingFleetProvider struct {
	settings.Provider
	mu    sync.Mutex
	calls int
}

func (provider *countingFleetProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	provider.mu.Unlock()
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

func (provider *countingFleetProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

type fleetInvalidationSource struct {
	mu            sync.Mutex
	subscriptions int
	messages      chan settings.Invalidation
	errors        chan error
}

func newFleetInvalidationSource() *fleetInvalidationSource {
	return &fleetInvalidationSource{
		messages: make(chan settings.Invalidation, 16), errors: make(chan error, 1),
	}
}

func (source *fleetInvalidationSource) Watch(context.Context, int) (<-chan settings.Invalidation, <-chan error, error) {
	source.mu.Lock()
	source.subscriptions++
	messages, errorsOut := source.messages, source.errors
	source.mu.Unlock()
	return messages, errorsOut, nil
}

func (source *fleetInvalidationSource) send(t testing.TB, event settings.Invalidation) {
	t.Helper()
	select {
	case source.messages <- event:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out delivering fake invalidation")
	}
}

func (source *fleetInvalidationSource) disconnect() {
	source.mu.Lock()
	close(source.messages)
	close(source.errors)
	source.messages = make(chan settings.Invalidation, 16)
	source.errors = make(chan error, 1)
	source.mu.Unlock()
}

func (source *fleetInvalidationSource) waitForSubscriptions(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		source.mu.Lock()
		got := source.subscriptions
		source.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("subscriptions did not reach %d", want)
}

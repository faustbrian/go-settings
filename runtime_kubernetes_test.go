package settings_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestKubernetesReplicaFleetConvergesAfterLostAndReorderedInvalidations(t *testing.T) {
	key := settings.NewKey("fleet", "rollout-generation", settings.IntCodec{})
	durable := memory.New()
	first, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "initial rollout",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := &fanoutFleetSource{}
	runtimes := make([]*settings.Runtime, 3)
	for index := range runtimes {
		runtime, runtimeErr := settings.NewRuntime(settings.RuntimeConfig{
			Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
			Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
			RefreshInterval: 50 * time.Millisecond,
			Invalidations:   source, WatchBuffer: 4, ReconnectDelay: 10 * time.Millisecond,
			InvalidationDebounce: 10 * time.Millisecond,
			Policies: map[settings.SettingClass]settings.ClassPolicy{
				settings.ClassStandard: {
					FreshFor: time.Second, MaxStaleness: time.Second,
					OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
					OnExpired: settings.FailClosed,
				},
			},
		})
		if runtimeErr != nil {
			t.Fatal(runtimeErr)
		}
		runtimes[index] = runtime
		if startErr := runtime.Start(t.Context()); startErr != nil {
			t.Fatal(startErr)
		}
	}
	t.Cleanup(func() {
		for _, runtime := range runtimes {
			if closeErr := runtime.Close(context.Background()); closeErr != nil {
				t.Error(closeErr)
			}
		}
	})
	source.waitForSubscribers(t, len(runtimes))

	second, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(2), settings.Change{
		Actor: "operator", Reason: "advance rollout",
	})
	if err != nil {
		t.Fatal(err)
	}
	source.sendTo(settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           settings.Global(), Key: key.StableID(), Version: second.Version, State: second.State,
	}, 0, 1)
	assertEventuallyGeneration(t, runtimes[0], key, 2)
	assertEventuallyGeneration(t, runtimes[1], key, 2)
	if deliveries := source.deliveriesTo(2); deliveries != 0 {
		t.Fatalf("lost-event replica received %d invalidations", deliveries)
	}

	source.sendTo(settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           settings.Global(), Key: key.StableID(), Version: first.Version, State: first.State,
	}, 0, 1)
	assertRuntimeGeneration(t, runtimes[0], key, 2)
	assertRuntimeGeneration(t, runtimes[1], key, 2)
	assertEventuallyGeneration(t, runtimes[2], key, 2)
}

type fanoutFleetSource struct {
	mu          sync.Mutex
	subscribers []chan settings.Invalidation
	deliveries  map[int]int
}

func (source *fanoutFleetSource) Watch(context.Context, int) (<-chan settings.Invalidation, <-chan error, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.deliveries == nil {
		source.deliveries = make(map[int]int)
	}
	events := make(chan settings.Invalidation, 4)
	source.subscribers = append(source.subscribers, events)
	return events, make(chan error), nil
}

func (source *fanoutFleetSource) sendTo(event settings.Invalidation, subscribers ...int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	for _, subscriber := range subscribers {
		source.subscribers[subscriber] <- event
		source.deliveries[subscriber]++
	}
}

func (source *fanoutFleetSource) deliveriesTo(subscriber int) int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.deliveries[subscriber]
}

func (source *fanoutFleetSource) waitForSubscribers(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		source.mu.Lock()
		got := len(source.subscribers)
		source.mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("watch subscribers did not reach %d", want)
}

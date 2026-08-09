package settings_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestRuntimeCoordinatesConcurrentStartupAndShutdown(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "startup coordination",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &startupBlockingProvider{
		Provider: durable, entered: make(chan struct{}), release: make(chan struct{}),
	}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	started := make(chan error, 1)
	go func() { started <- runtime.Start(context.Background()) }()
	receiveFleet(t, provider.entered, "startup provider entry")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Start(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled concurrent start = %v", err)
	}
	if err := runtime.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled close during start = %v", err)
	}
	secondStart := make(chan error, 1)
	go func() { secondStart <- runtime.Start(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	select {
	case err := <-secondStart:
		t.Fatalf("concurrent start returned before initial startup completed: %v", err)
	default:
	}
	close(provider.release)
	if err := receiveFleet(t, started, "startup result"); err != nil {
		t.Fatal(err)
	}
	if err := receiveFleet(t, secondStart, "concurrent startup result"); !errors.Is(err, settings.ErrRuntimeStarted) {
		t.Fatalf("concurrent duplicate start = %v", err)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCloseWaitsForStartupThenShutsDown(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "startup shutdown ordering",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &startupBlockingProvider{
		Provider: durable, entered: make(chan struct{}), release: make(chan struct{}),
	}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	started := make(chan error, 1)
	go func() { started <- runtime.Start(context.Background()) }()
	receiveFleet(t, provider.entered, "startup provider entry")
	closed := make(chan error, 1)
	go func() { closed <- runtime.Close(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	select {
	case err := <-closed:
		t.Fatalf("close returned before startup completed: %v", err)
	default:
	}
	close(provider.release)
	if err := receiveFleet(t, started, "startup result"); err != nil {
		t.Fatal(err)
	}
	if err := receiveFleet(t, closed, "close after startup"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); !errors.Is(err, settings.ErrRuntimeClosed) {
		t.Fatalf("restart after coordinated close = %v", err)
	}
}

func TestRuntimeRejectsStaleCachedStateBeforeDurableStartupRefresh(t *testing.T) {
	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "cached generation",
	}); err != nil {
		t.Fatal(err)
	}
	old, err := settings.Capture(t.Context(), durable, settings.Chain(settings.Global()), key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := old.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(2), settings.Change{
		Actor: "operator", Reason: "durable generation",
	}); err != nil {
		t.Fatal(err)
	}
	store := &fleetSnapshotStore{data: encoded, present: true}
	runtime := mustRuntimeWithStore(t, durable, store, key)
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	assertRuntimeGeneration(t, runtime, key, 2)
}

func TestRuntimeReportsCancellationAfterACompletedStartupCapture(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "startup cancellation",
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	provider := &cancelAfterReadProvider{Provider: durable, cancel: cancel}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	if err := runtime.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation = %v", err)
	}
}

func TestRuntimeCloseHonorsItsDeadlineWhileRefreshDrains(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "shutdown drain",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &stubbornRefreshProvider{
		Provider: durable, entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{}),
	}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Minute,
		RefreshInterval: 10 * time.Millisecond,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
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
	receiveFleet(t, provider.entered, "stubborn refresh entry")
	closed, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Close(closed); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded close = %v", err)
	}
	close(provider.release)
	receiveFleet(t, provider.finished, "stubborn refresh drain")
}

func TestRuntimeReconnectsAfterWatcherErrorSignals(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "watch errors",
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		signal error
	}{
		{name: "error value", signal: errors.New("valkey subscription failed")},
		{name: "closed error stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &errorFleetSource{signal: test.signal, subscriptions: make(chan struct{}, 2)}
			runtime := mustRuntimeWithSource(t, durable, source, key)
			if err := runtime.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			receiveFleet(t, source.subscriptions, "initial watch subscription")
			receiveFleet(t, source.subscriptions, "reconnected watch subscription")
			if err := runtime.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeCancellationDrainsRefreshAndReconnectLoops(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "loop cancellation",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &cancelingRefreshProvider{
		Provider: durable, entered: make(chan struct{}), finished: make(chan struct{}),
	}
	source := &closingFleetSource{subscriptions: make(chan struct{}, 1)}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		RefreshInterval: 10 * time.Millisecond, Invalidations: source, WatchBuffer: 1,
		ReconnectDelay: time.Minute, InvalidationDebounce: time.Minute,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
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
	receiveFleet(t, source.subscriptions, "closing watch subscription")
	receiveFleet(t, provider.entered, "background refresh entry")
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	receiveFleet(t, provider.finished, "background refresh drain")
}

func TestRuntimeBoundsInvalidationStormsAndReconcilesMalformedHints(t *testing.T) {
	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "invalidation storm",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &countingFleetProvider{Provider: durable}
	source := newFleetInvalidationSource()
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		Invalidations: source, WatchBuffer: 1, ReconnectDelay: 10 * time.Millisecond,
		InvalidationDebounce: 20 * time.Millisecond,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
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
	source.waitForSubscriptions(t, 1)
	for version := uint64(1); version <= 20; version++ {
		source.send(t, settings.Invalidation{
			ProtocolVersion: settings.InvalidationProtocolVersion,
			Scope:           settings.Global(), Key: key.StableID(), Version: version, State: settings.StateValue,
		})
	}
	source.send(t, settings.Invalidation{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           settings.Global(), Key: key.StableID(), Version: 21, State: settings.State(255),
	})
	deadline := time.Now().Add(time.Second)
	for provider.Calls() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls := provider.Calls(); calls < 2 || calls > 3 {
		t.Fatalf("storm refresh calls = %d, want bounded reconciliation", calls)
	}
}

func TestRuntimeClampsCallerJitterWithoutLosingStartup(t *testing.T) {
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "jitter clamp",
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		jitter func(time.Duration) time.Duration
	}{
		{name: "negative", jitter: func(time.Duration) time.Duration { return -time.Second }},
		{name: "excessive", jitter: func(max time.Duration) time.Duration { return max + time.Second }},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := settings.NewRuntime(settings.RuntimeConfig{
				Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
				Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
				MaxJitter: 10 * time.Millisecond, Jitter: test.jitter,
				Policies: map[settings.SettingClass]settings.ClassPolicy{
					settings.ClassStandard: {
						FreshFor: time.Minute, MaxStaleness: time.Minute,
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
			if !runtime.Ready() {
				t.Fatal("jitter-clamped runtime not ready")
			}
			if err := runtime.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimePeriodicRefreshRecoversAfterATransientFailure(t *testing.T) {
	key := settings.NewKey("fleet", "periodic-recovery", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "before", settings.Change{
		Actor: "operator", Reason: "seed periodic recovery",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &transientFleetProvider{
		Provider: durable, failCall: 2, calls: make(chan int, 8),
	}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		RefreshInterval: 10 * time.Millisecond,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
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
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "after", settings.Change{
		Actor: "operator", Reason: "prove periodic recovery",
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for call := 0; call < 3; {
		select {
		case call = <-provider.calls:
		case <-deadline.C:
			t.Fatal("periodic refresh stopped after a transient failure")
		}
	}
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		result, resolveErr := settings.ResolveCurrent(runtime, key)
		if resolveErr == nil && result.Value == "after" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("periodic refresh did not publish recovered durable state")
}

type startupBlockingProvider struct {
	settings.Provider
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

type transientFleetProvider struct {
	settings.Provider
	mu       sync.Mutex
	call     int
	failCall int
	calls    chan int
}

func (provider *transientFleetProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.call++
	call := provider.call
	provider.mu.Unlock()
	select {
	case provider.calls <- call:
	default:
	}
	if call == provider.failCall {
		return nil, errors.New("transient durable refresh failure")
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

func (provider *startupBlockingProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	blocked := false
	provider.once.Do(func() { blocked = true; close(provider.entered) })
	if blocked {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-provider.release:
		}
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

type cancelingRefreshProvider struct {
	settings.Provider
	mu       sync.Mutex
	calls    int
	entered  chan struct{}
	finished chan struct{}
}

func (provider *cancelingRefreshProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call == 1 {
		return provider.Provider.BulkGet(ctx, scopes, keys)
	}
	close(provider.entered)
	<-ctx.Done()
	close(provider.finished)
	return nil, ctx.Err()
}

type closingFleetSource struct{ subscriptions chan struct{} }

func (source *closingFleetSource) Watch(context.Context, int) (<-chan settings.Invalidation, <-chan error, error) {
	select {
	case source.subscriptions <- struct{}{}:
	default:
	}
	events := make(chan settings.Invalidation)
	errorsOut := make(chan error)
	close(events)
	close(errorsOut)
	return events, errorsOut, nil
}

type cancelAfterReadProvider struct {
	settings.Provider
	cancel context.CancelFunc
}

func (provider *cancelAfterReadProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	records, err := provider.Provider.BulkGet(ctx, scopes, keys)
	provider.cancel()
	return records, err
}

type stubbornRefreshProvider struct {
	settings.Provider
	mu       sync.Mutex
	calls    int
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func (provider *stubbornRefreshProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call > 1 {
		close(provider.entered)
		<-provider.release
		close(provider.finished)
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

type errorFleetSource struct {
	signal        error
	subscriptions chan struct{}
}

func (source *errorFleetSource) Watch(context.Context, int) (<-chan settings.Invalidation, <-chan error, error) {
	events := make(chan settings.Invalidation)
	errorsOut := make(chan error, 1)
	if source.signal != nil {
		errorsOut <- source.signal
	}
	close(errorsOut)
	select {
	case source.subscriptions <- struct{}{}:
	default:
	}
	return events, errorsOut, nil
}

func mustRuntimeWithSource(t *testing.T, provider settings.Provider, source settings.InvalidationSource, key settings.Definition) *settings.Runtime {
	t.Helper()
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		Invalidations: source, WatchBuffer: 1, ReconnectDelay: 10 * time.Millisecond,
		InvalidationDebounce: 10 * time.Millisecond,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
				OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func mustRuntimeWithStore(t *testing.T, provider settings.Provider, store settings.SnapshotStore, key settings.Definition) *settings.Runtime {
	t.Helper()
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second, SnapshotStore: store,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
				OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

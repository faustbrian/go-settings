package settings_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestRuntimeRejectsEveryUnboundedOrImplicitPolicyDimension(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	valid := func() settings.RuntimeConfig {
		return settings.RuntimeConfig{
			Provider: memory.New(), Chain: settings.Chain(settings.Global()),
			Definitions: []settings.Definition{key}, Provenance: settings.ProvenancePostgreSQL,
			RefreshTimeout: time.Second,
			Policies: map[settings.SettingClass]settings.ClassPolicy{
				settings.ClassStandard: {
					FreshFor: time.Second, MaxStaleness: time.Minute,
					OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
					OnExpired: settings.FailClosed,
				},
			},
		}
	}
	source := newFleetInvalidationSource()
	invalid := []struct {
		name   string
		mutate func(*settings.RuntimeConfig)
	}{
		{"nil provider", func(config *settings.RuntimeConfig) { config.Provider = nil }},
		{"non-snapshot provider", func(config *settings.RuntimeConfig) {
			config.Provider = noSnapshotFleetProvider{Provider: memory.New()}
		}},
		{"empty chain", func(config *settings.RuntimeConfig) { config.Chain = settings.ResolutionChain{} }},
		{"empty definitions", func(config *settings.RuntimeConfig) { config.Definitions = nil }},
		{"too many definitions", func(config *settings.RuntimeConfig) { config.Definitions = make([]settings.Definition, 10_001) }},
		{"zero refresh timeout", func(config *settings.RuntimeConfig) { config.RefreshTimeout = 0 }},
		{"excessive refresh timeout", func(config *settings.RuntimeConfig) { config.RefreshTimeout = 5*time.Minute + 1 }},
		{"unknown provenance", func(config *settings.RuntimeConfig) { config.Provenance = "unknown" }},
		{"default provenance", func(config *settings.RuntimeConfig) { config.Provenance = settings.ProvenanceDefaults }},
		{"negative interval", func(config *settings.RuntimeConfig) { config.RefreshInterval = -1 }},
		{"tiny interval", func(config *settings.RuntimeConfig) { config.RefreshInterval = 10*time.Millisecond - 1 }},
		{"excessive interval", func(config *settings.RuntimeConfig) { config.RefreshInterval = 24*time.Hour + 1 }},
		{"negative jitter", func(config *settings.RuntimeConfig) { config.MaxJitter = -1 }},
		{"excessive jitter", func(config *settings.RuntimeConfig) {
			config.MaxJitter = time.Hour + 1
			config.Jitter = func(time.Duration) time.Duration { return 0 }
		}},
		{"implicit jitter", func(config *settings.RuntimeConfig) { config.MaxJitter = time.Second }},
		{"empty watcher", func(config *settings.RuntimeConfig) {
			config.Invalidations = source
			config.ReconnectDelay = 10 * time.Millisecond
			config.InvalidationDebounce = 10 * time.Millisecond
		}},
		{"excessive watcher", func(config *settings.RuntimeConfig) {
			config.Invalidations = source
			config.WatchBuffer = 10_001
			config.ReconnectDelay = 10 * time.Millisecond
			config.InvalidationDebounce = 10 * time.Millisecond
		}},
		{"tiny reconnect", func(config *settings.RuntimeConfig) {
			config.Invalidations = source
			config.WatchBuffer = 1
			config.ReconnectDelay = 10*time.Millisecond - 1
			config.InvalidationDebounce = 10 * time.Millisecond
		}},
		{"excessive reconnect", func(config *settings.RuntimeConfig) {
			config.Invalidations = source
			config.WatchBuffer = 1
			config.ReconnectDelay = time.Minute + 1
			config.InvalidationDebounce = 10 * time.Millisecond
		}},
		{"tiny debounce", func(config *settings.RuntimeConfig) {
			config.Invalidations = source
			config.WatchBuffer = 1
			config.ReconnectDelay = 10 * time.Millisecond
			config.InvalidationDebounce = 10*time.Millisecond - 1
		}},
		{"excessive debounce", func(config *settings.RuntimeConfig) {
			config.Invalidations = source
			config.WatchBuffer = 1
			config.ReconnectDelay = 10 * time.Millisecond
			config.InvalidationDebounce = time.Minute + 1
		}},
		{"nil definition", func(config *settings.RuntimeConfig) { config.Definitions = []settings.Definition{nil} }},
		{"invalid definition", func(config *settings.RuntimeConfig) {
			config.Definitions = []settings.Definition{settings.NewKey("", "mode", settings.StringCodec{})}
		}},
		{"duplicate definition", func(config *settings.RuntimeConfig) { config.Definitions = []settings.Definition{key, key} }},
		{"missing class policy", func(config *settings.RuntimeConfig) { config.Policies = nil }},
		{"unknown policy class", func(config *settings.RuntimeConfig) {
			config.Policies[settings.SettingClass(255)] = config.Policies[settings.ClassStandard]
		}},
		{"negative fresh bound", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.FreshFor = -1
			config.Policies[settings.ClassStandard] = policy
		}},
		{"reversed stale bound", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.MaxStaleness = policy.FreshFor - 1
			config.Policies[settings.ClassStandard] = policy
		}},
		{"excessive stale bound", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.MaxStaleness = 24*time.Hour + 1
			config.Policies[settings.ClassStandard] = policy
		}},
		{"unknown unavailable action", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.OnUnavailable = 255
			config.Policies[settings.ClassStandard] = policy
		}},
		{"unknown stale action", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.OnStale = 255
			config.Policies[settings.ClassStandard] = policy
		}},
		{"unknown expired action", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.OnExpired = 255
			config.Policies[settings.ClassStandard] = policy
		}},
		{"unavailable serves stale", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.OnUnavailable = settings.ServeLastKnownGood
			config.Policies[settings.ClassStandard] = policy
		}},
		{"expired serves stale", func(config *settings.RuntimeConfig) {
			policy := config.Policies[settings.ClassStandard]
			policy.OnExpired = settings.ServeLastKnownGood
			config.Policies[settings.ClassStandard] = policy
		}},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			config := valid()
			test.mutate(&config)
			if _, err := settings.NewRuntime(config); err == nil {
				t.Fatal("invalid runtime configuration accepted")
			}
		})
	}

	boundary := valid()
	boundary.RefreshTimeout = 5 * time.Minute
	boundary.RefreshInterval = 24 * time.Hour
	boundary.MaxJitter = time.Hour
	boundary.Jitter = func(max time.Duration) time.Duration { return max }
	boundary.Invalidations = source
	boundary.WatchBuffer = 10_000
	boundary.ReconnectDelay = time.Minute
	boundary.InvalidationDebounce = time.Minute
	policy := boundary.Policies[settings.ClassStandard]
	policy.FreshFor = 24 * time.Hour
	policy.MaxStaleness = 24 * time.Hour
	boundary.Policies[settings.ClassStandard] = policy
	if _, err := settings.NewRuntime(boundary); err != nil {
		t.Fatalf("valid exact boundaries rejected: %v", err)
	}

	exactDefinitions := valid()
	exactDefinitions.Definitions = make([]settings.Definition, 10_000)
	for index := range exactDefinitions.Definitions {
		exactDefinitions.Definitions[index] = settings.NewKey("fleet-boundary", "key-"+strconv.Itoa(index), settings.StringCodec{})
	}
	if _, err := settings.NewRuntime(exactDefinitions); err != nil {
		t.Fatalf("exact definition bound rejected: %v", err)
	}

	exactMinimums := valid()
	exactMinimums.Invalidations = source
	exactMinimums.WatchBuffer = 1
	exactMinimums.ReconnectDelay = 10 * time.Millisecond
	exactMinimums.InvalidationDebounce = 10 * time.Millisecond
	minimumPolicy := exactMinimums.Policies[settings.ClassStandard]
	minimumPolicy.FreshFor = 0
	minimumPolicy.MaxStaleness = 0
	exactMinimums.Policies[settings.ClassStandard] = minimumPolicy
	if _, err := settings.NewRuntime(exactMinimums); err != nil {
		t.Fatalf("valid exact minimums rejected: %v", err)
	}
}

func TestRuntimeDelegatesRetriesAndBoundsStartupJitter(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "retry refresh",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &failFirstBulkProvider{Provider: durable}
	var attempts int
	executor := settings.RefreshExecutorFunc(func(ctx context.Context, operation func(context.Context) error) error {
		for attempts < 2 {
			attempts++
			if err := operation(ctx); err != nil {
				continue
			}
			return nil
		}
		return errors.New("retry attempts exhausted")
	})
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second, Executor: executor,
		MaxJitter: 20 * time.Millisecond, Jitter: func(max time.Duration) time.Duration { return max },
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
	started := time.Now()
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond || attempts != 2 || provider.Calls() != 2 {
		t.Fatalf("startup elapsed = %v, attempts = %d, calls = %d", elapsed, attempts, provider.Calls())
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

}

func TestRuntimeLifecycleAndSnapshotCacheFailuresStayObservable(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "mode", settings.StringCodec{}, settings.WithDefault("safe"))
	closed := mustRuntime(t, memory.New(), systemFleetClock{}, key)
	if err := closed.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := closed.Start(t.Context()); !errors.Is(err, settings.ErrRuntimeClosed) {
		t.Fatalf("start after close = %v", err)
	}

	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "lifecycle",
	}); err != nil {
		t.Fatal(err)
	}
	store := &failingFleetSnapshotStore{}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second, SnapshotStore: store,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
				OnUnavailable: settings.UseDefault, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.UseDefault,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); !errors.Is(err, settings.ErrRuntimeStarted) {
		t.Fatalf("duplicate start = %v", err)
	}
	store.failSave = true
	if err := runtime.Refresh(t.Context()); err == nil || !strings.Contains(err.Error(), "save snapshot cache") {
		t.Fatalf("snapshot save error = %v", err)
	}
	if result, err := settings.ResolveCurrent(runtime, key); err != nil || result.Value != "safe" {
		t.Fatalf("active snapshot after save failure = (%+v, %v)", result, err)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	loadFailure := errors.New("snapshot cache unavailable")
	store.failLoad = loadFailure
	blocked, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: unavailableFleetProvider{Provider: durable},
		Chain:    settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second, SnapshotStore: store,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
				OnUnavailable: settings.FailClosed, OnStale: settings.FailClosed,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := blocked.Start(t.Context()); !errors.Is(err, loadFailure) {
		t.Fatalf("snapshot load failure = %v", err)
	}
}

func TestRuntimeRejectsAnOversizedSnapshotCacheDocumentAfterActivation(t *testing.T) {
	t.Parallel()

	first := settings.NewKey("fleet", "large-first", settings.StringCodec{})
	second := settings.NewKey("fleet", "large-second", settings.StringCodec{})
	durable := memory.New()
	large := strings.Repeat("x", 800_000)
	for _, key := range []settings.Key[string]{first, second} {
		if _, err := settings.Set(t.Context(), durable, settings.Global(), key, large, settings.Change{
			Actor: "operator", Reason: "bounded snapshot cache",
		}); err != nil {
			t.Fatal(err)
		}
	}
	store := &failingFleetSnapshotStore{}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()),
		Definitions: []settings.Definition{first, second},
		Provenance:  settings.ProvenancePostgreSQL, RefreshTimeout: time.Second, SnapshotStore: store,
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
	if err := runtime.Refresh(t.Context()); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("oversized snapshot cache error = %v", err)
	}
	result, err := settings.ResolveCurrent(runtime, first)
	if err != nil || result.Value != large {
		t.Fatalf("activated durable state = (%d bytes, %v)", len(result.Value), err)
	}
}

func TestRuntimeUnavailableExpiredAndDefaultPoliciesRemainDistinct(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	clock := &fleetClock{now: now}
	withoutDefault := settings.NewKey("fleet", "required", settings.StringCodec{})
	withDefault := settings.NewKey("fleet", "optional", settings.StringCodec{}, settings.WithDefault("safe"))
	durable := memory.NewWithClock(clock.Now)
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()),
		Definitions: []settings.Definition{withoutDefault, withDefault},
		Clock:       clock, Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: 2 * time.Second,
				OnUnavailable: settings.UseDefault, OnStale: settings.UseDefault,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.ResolveCurrent(runtime, withoutDefault); !errors.Is(err, settings.ErrDefaultUnavailable) {
		t.Fatalf("unavailable required setting = %v", err)
	}
	defaulted, err := settings.ResolveCurrent(runtime, withDefault)
	if err != nil || defaulted.Value != "safe" || defaulted.Freshness != settings.Default {
		t.Fatalf("unavailable default = (%+v, %v)", defaulted, err)
	}
	if runtime.Ready() {
		t.Fatal("runtime ready without a required setting")
	}
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !runtime.Ready() {
		t.Fatal("fresh empty snapshot with explicit missing outcomes was not ready")
	}
	clock.now = now.Add(3 * time.Second)
	if _, err := settings.ResolveCurrent(runtime, withDefault); !errors.Is(err, settings.ErrSnapshotExpired) {
		t.Fatalf("expired setting = %v", err)
	}
}

func TestRuntimeWriteErrorsPreserveCommitAndReconciliationState(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	change := settings.Change{Actor: "operator", Reason: "write boundary"}
	mutation, err := settings.PrepareSet(settings.Global(), key, int64(2), nil, change)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("no durable acknowledgement", func(t *testing.T) {
		provider := zeroApplyFleetProvider{Provider: memory.New()}
		runtime := mustRuntime(t, provider, systemFleetClock{}, key)
		record, err := runtime.Apply(t.Context(), mutation)
		if err == nil || record.Version != 0 || runtime.Ready() {
			t.Fatalf("uncommitted write = (%+v, %v, ready %v)", record, err, runtime.Ready())
		}
	})

	t.Run("malformed successful acknowledgement", func(t *testing.T) {
		provider := emptyAcknowledgementFleetProvider{Provider: memory.New()}
		runtime := mustRuntime(t, provider, systemFleetClock{}, key)
		record, err := runtime.Apply(t.Context(), mutation)
		if !errors.Is(err, settings.ErrInvalidValue) || record.Version != 0 || runtime.Ready() {
			t.Fatalf("malformed acknowledgement = (%+v, %v, ready %v)", record, err, runtime.Ready())
		}
	})

	t.Run("generic post-commit failure", func(t *testing.T) {
		durable := memory.New()
		if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), change); err != nil {
			t.Fatal(err)
		}
		provider := genericCommitFleetProvider{Provider: durable}
		runtime := mustRuntime(t, provider, systemFleetClock{}, key)
		if err := runtime.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		record, err := runtime.Apply(t.Context(), mutation)
		var committed *settings.CommittedWriteError
		if !errors.As(err, &committed) || !committed.Committed || committed.Unwrap() == nil ||
			!strings.Contains(committed.Error(), "durable commit") || record.Version != 2 {
			t.Fatalf("committed write = (%+v, %#v)", record, err)
		}
		assertRuntimeGeneration(t, runtime, key, 2)
	})

	t.Run("reconciliation failure", func(t *testing.T) {
		durable := memory.New()
		if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), change); err != nil {
			t.Fatal(err)
		}
		provider := &failAfterApplyFleetProvider{Provider: durable}
		runtime := mustRuntime(t, provider, systemFleetClock{}, key)
		if err := runtime.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		provider.failBulk = true
		record, err := runtime.Apply(t.Context(), mutation)
		var committed *settings.CommittedWriteError
		if !errors.As(err, &committed) || record.Version != 2 {
			t.Fatalf("reconciliation failure = (%+v, %#v)", record, err)
		}
		assertRuntimeGeneration(t, runtime, key, 1)
	})
}

func TestRuntimeCancellationStopsStartupRefreshAndReconnectWaits(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "cancellation",
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		MaxJitter: time.Second, Jitter: func(time.Duration) time.Duration { return time.Second },
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup = %v", err)
	}
}

type failFirstBulkProvider struct {
	settings.Provider
	mu    sync.Mutex
	calls int
}

func (provider *failFirstBulkProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call == 1 {
		return nil, errors.New("postgres failover")
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

func (provider *failFirstBulkProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

type failingFleetSnapshotStore struct {
	failSave bool
	failLoad error
}

func (store *failingFleetSnapshotStore) Load(context.Context) ([]byte, bool, error) {
	return nil, false, store.failLoad
}
func (store *failingFleetSnapshotStore) Save(context.Context, []byte) error {
	if store.failSave {
		return errors.New("disk unavailable")
	}
	return nil
}

type zeroApplyFleetProvider struct{ settings.Provider }

func (zeroApplyFleetProvider) Apply(context.Context, settings.Mutation) (settings.Record, error) {
	return settings.Record{}, errors.New("postgres rejected write")
}

type emptyAcknowledgementFleetProvider struct{ settings.Provider }

func (emptyAcknowledgementFleetProvider) Apply(context.Context, settings.Mutation) (settings.Record, error) {
	return settings.Record{}, nil
}

type noSnapshotFleetProvider struct{ settings.Provider }

func (noSnapshotFleetProvider) Capabilities() settings.Capabilities { return settings.Capabilities{} }

type genericCommitFleetProvider struct{ settings.Provider }

func (provider genericCommitFleetProvider) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	record, err := provider.Provider.Apply(ctx, mutation)
	if err != nil {
		return record, err
	}
	return record, errors.New("valkey fanout unavailable")
}

type failAfterApplyFleetProvider struct {
	settings.Provider
	failBulk bool
}

func (provider *failAfterApplyFleetProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	if provider.failBulk {
		return nil, errors.New("postgres failover")
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

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

func TestRuntimeEnforcesBoundedPolicyPerSettingClass(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	clock := &fleetClock{now: now}
	standard := settings.NewKey("fleet", "theme", settings.StringCodec{}, settings.WithDefault("system"))
	secret := settings.NewKey("fleet", "credential", settings.StringCodec{}, settings.WithSensitive[string]())
	security := settings.NewKey("fleet", "authorization-mode", settings.StringCodec{},
		settings.WithClass[string](settings.ClassSecuritySensitive), settings.WithDefault("deny"),
	)
	durable := memory.NewWithClock(clock.Now)
	for _, mutation := range []settings.Mutation{
		mustPrepareSet(t, standard, "dark"),
		mustPrepareSet(t, secret, "encrypted-secret"),
		mustPrepareSet(t, security, "allow"),
	} {
		if _, err := durable.Apply(t.Context(), mutation); err != nil {
			t.Fatal(err)
		}
	}

	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider:       durable,
		Chain:          settings.Chain(settings.Global()),
		Definitions:    []settings.Definition{standard, secret, security},
		Clock:          clock,
		Provenance:     settings.ProvenancePostgreSQL,
		RefreshTimeout: time.Second,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.UseDefault, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.UseDefault,
			},
			settings.ClassSecret: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.FailClosed,
				OnExpired: settings.FailClosed,
			},
			settings.ClassSecuritySensitive: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.UseDefault, OnStale: settings.UseDefault,
				OnExpired: settings.UseDefault,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	fresh, err := settings.ResolveCurrent(runtime, standard)
	if err != nil || fresh.Value != "dark" || fresh.Freshness != settings.Fresh {
		t.Fatalf("fresh result = (%+v, %v)", fresh, err)
	}
	clock.now = now.Add(time.Second)
	atFreshBoundary, err := settings.ResolveCurrent(runtime, standard)
	if err != nil || atFreshBoundary.Freshness != settings.Fresh || !runtime.Ready() {
		t.Fatalf("fresh boundary = (%+v, %v, ready %v)", atFreshBoundary, err, runtime.Ready())
	}

	clock.now = now.Add(3 * time.Second)
	stale, err := settings.ResolveCurrent(runtime, standard)
	if err != nil || stale.Value != "dark" || stale.Freshness != settings.Stale ||
		stale.Snapshot.Age != 3*time.Second {
		t.Fatalf("stale result = (%+v, %v)", stale, err)
	}
	if _, err := settings.ResolveCurrent(runtime, secret); !errors.Is(err, settings.ErrSnapshotStale) {
		t.Fatalf("stale secret error = %v", err)
	}
	if runtime.Ready() {
		t.Fatal("runtime remained ready when a stale secret was fail-closed")
	}
	defaulted, err := settings.ResolveCurrent(runtime, security)
	if err != nil || defaulted.Value != "deny" || defaulted.Freshness != settings.Default ||
		defaulted.Snapshot.Provenance != settings.ProvenanceDefaults {
		t.Fatalf("security default = (%+v, %v)", defaulted, err)
	}
	clock.now = now.Add(5 * time.Second)
	atStaleBoundary, err := settings.ResolveCurrent(runtime, standard)
	if err != nil || atStaleBoundary.Freshness != settings.Stale {
		t.Fatalf("stale boundary = (%+v, %v)", atStaleBoundary, err)
	}

	clock.now = now.Add(6 * time.Second)
	expired, err := settings.ResolveCurrent(runtime, standard)
	if err != nil || expired.Value != "system" || expired.Freshness != settings.Default {
		t.Fatalf("expired standard = (%+v, %v)", expired, err)
	}
	if runtime.Ready() {
		t.Fatal("runtime remained ready while the secret class was fail-closed")
	}
}

func TestRuntimeRejectsCallerDefinitionThatWeakensRegisteredPolicy(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	clock := &fleetClock{now: now}
	registered := settings.NewKey("fleet", "credential", settings.StringCodec{},
		settings.WithSensitive[string](),
	)
	registeredDefault := settings.NewKey("fleet", "authorization-default", settings.StringCodec{},
		settings.WithDefault("deny"),
	)
	durable := memory.NewWithClock(clock.Now)
	if _, err := durable.Apply(t.Context(), mustPrepareSet(t, registered, "secret")); err != nil {
		t.Fatal(err)
	}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()),
		Definitions: []settings.Definition{registered, registeredDefault}, Clock: clock,
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.UseDefault, OnStale: settings.UseDefault,
				OnExpired: settings.UseDefault,
			},
			settings.ClassSecret: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.FailClosed,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	injectedDefault := settings.NewKey("fleet", "authorization-default", settings.StringCodec{},
		settings.WithDefault("allow"),
	)
	defaulted, err := settings.ResolveCurrent(runtime, injectedDefault)
	if err != nil || defaulted.Value != "deny" || defaulted.Status != settings.StatusDefaulted {
		t.Fatalf("caller-injected default result = (%+v, %v)", defaulted, err)
	}
	clock.now = now.Add(2 * time.Second)

	weakened := settings.NewKey("fleet", "credential", settings.StringCodec{},
		settings.WithDefault("attacker-selected-default"),
	)
	if result, err := settings.ResolveCurrent(runtime, weakened); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("weakened definition result = (%+v, %v)", result, err)
	}

	rejectDefault := func(string) error { return errors.New("caller validation") }
	for name, candidate := range map[string]settings.Key[string]{
		"unregistered": settings.NewKey("fleet", "other", settings.StringCodec{}),
		"invalid":      settings.NewKey("fleet", "", settings.StringCodec{}),
		"codec id": settings.NewKey("fleet", "authorization-default",
			failingStringCodec{id: "other", version: 1}),
		"codec version": settings.NewKey("fleet", "authorization-default",
			failingStringCodec{id: "string", version: 2}),
		"sensitivity": settings.NewKey("fleet", "authorization-default", settings.StringCodec{},
			settings.WithSensitive[string](), settings.WithClass[string](settings.ClassStandard)),
		"class": settings.NewKey("fleet", "credential", settings.StringCodec{},
			settings.WithClass[string](settings.ClassSecuritySensitive)),
		"default decode": settings.NewKey("fleet", "authorization-default",
			failingStringCodec{id: "string", version: 1, decode: true}),
		"default validation": settings.NewKey("fleet", "authorization-default", settings.StringCodec{},
			settings.WithValidation(rejectDefault)),
	} {
		if _, err := settings.ResolveCurrent(runtime, candidate); !errors.Is(err, settings.ErrInvalidDefinition) {
			t.Errorf("%s definition error = %v", name, err)
		}
	}
}

func TestRuntimeReadinessHonorsExactStaleBoundariesForOneClass(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	clock := &fleetClock{now: now}
	key := settings.NewKey("fleet", "readiness-boundary", settings.StringCodec{})
	durable := memory.NewWithClock(clock.Now)
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "readiness boundary",
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Clock: clock, Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, age := range []time.Duration{time.Second, 3 * time.Second, 5 * time.Second} {
		clock.now = now.Add(age)
		if !runtime.Ready() {
			t.Fatalf("runtime not ready at bounded age %v", age)
		}
	}
	clock.now = now.Add(5*time.Second + 1)
	if runtime.Ready() {
		t.Fatal("runtime ready beyond maximum staleness")
	}
}

func TestRuntimeRefreshIsSingleFlightAndPreservesLastKnownGood(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	clock := &fleetClock{now: now}
	key := settings.NewKey("fleet", "mode", settings.StringCodec{},
		settings.WithValidation(func(value string) error {
			if value != "safe" {
				return errors.New("unsafe")
			}
			return nil
		}),
	)
	durable := memory.NewWithClock(clock.Now)
	if _, err := durable.Apply(t.Context(), mustPrepareSet(t, key, "safe")); err != nil {
		t.Fatal(err)
	}
	provider := &blockingFleetProvider{
		Provider: durable, entered: make(chan struct{}), release: make(chan struct{}),
	}
	runtime := mustRuntime(t, provider, clock, key)

	first := make(chan error, 1)
	go func() { first <- runtime.Refresh(context.Background()) }()
	receiveFleet(t, provider.entered, "initial refresh entry")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Refresh(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled single-flight waiter = %v", err)
	}
	second := make(chan error, 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		second <- runtime.Refresh(context.Background())
	}()
	receiveFleet(t, secondStarted, "single-flight waiter start")
	provider.assertCallsRemain(t, 1, 20*time.Millisecond)
	close(provider.release)
	if err := receiveFleet(t, first, "initial refresh result"); err != nil {
		t.Fatal(err)
	}
	if err := receiveFleet(t, second, "joined refresh result"); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("bulk calls = %d, want one", provider.calls)
	}

	provider.corrupt = true
	if err := runtime.Refresh(t.Context()); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("malformed refresh error = %v", err)
	}
	current, err := settings.ResolveCurrent(runtime, key)
	if err != nil || current.Value != "safe" {
		t.Fatalf("last known good after malformed refresh = (%+v, %v)", current, err)
	}
}

func TestRuntimeJoinedRefreshReturnsTheSharedFlightFailure(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "shared failure",
	}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("postgres failover")
	provider := &failedBlockingProvider{
		Provider: durable, failure: failure, entered: make(chan struct{}), release: make(chan struct{}),
	}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	first := make(chan error, 1)
	go func() { first <- runtime.Refresh(context.Background()) }()
	receiveFleet(t, provider.entered, "failing refresh entry")
	second := make(chan error, 1)
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		second <- runtime.Refresh(context.Background())
	}()
	receiveFleet(t, secondStarted, "failing joined refresh start")
	provider.assertCallsRemain(t, 1, 20*time.Millisecond)
	close(provider.release)
	if err := receiveFleet(t, first, "failing refresh result"); !errors.Is(err, failure) {
		t.Fatalf("initial flight error = %v", err)
	}
	if err := receiveFleet(t, second, "failing joined refresh result"); !errors.Is(err, failure) {
		t.Fatalf("joined flight error = %v", err)
	}
	if calls := provider.Calls(); calls != 1 {
		t.Fatalf("failed flight calls = %d, want one", calls)
	}
}

func TestRuntimeReconcilesAcknowledgedWritesAndRejectsVersionRegression(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	first, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "initial generation",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := &commitReportingProvider{Provider: durable}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}

	mutation := mustPrepareSet(t, key, int64(2))
	record, err := runtime.Apply(t.Context(), mutation)
	var committed *settings.CommittedWriteError
	if !errors.As(err, &committed) || !committed.Committed || record.Version != 2 {
		t.Fatalf("write result = (%+v, %#v)", record, err)
	}
	assertRuntimeGeneration(t, runtime, key, 2)

	provider.stale = first
	if err := runtime.Refresh(t.Context()); !errors.Is(err, settings.ErrNonMonotonicSnapshot) {
		t.Fatalf("regressing refresh error = %v", err)
	}
	assertRuntimeGeneration(t, runtime, key, 2)

	inherit := settings.Mutation{
		Scope: settings.Global(), Key: key.StableID(), Action: settings.ActionInherit,
		CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
		Change: settings.Change{Actor: "operator", Reason: "remove override"},
	}
	provider.stale = settings.Record{}
	if _, err := runtime.Apply(t.Context(), inherit); err == nil {
		t.Fatal("committed cache warning was hidden")
	}
	if result, err := settings.ResolveCurrent(runtime, key); err != nil || result.Status != settings.StatusMissing {
		t.Fatalf("inherited runtime result = (%+v, %v)", result, err)
	}
}

func TestRuntimeRejectsWritesOutsideItsRegisteredSnapshotBeforeCommit(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "bounded-write", settings.StringCodec{},
		settings.WithSensitive[string](),
		settings.WithValidation(func(value string) error {
			if value != "valid" {
				return errors.New("invalid value")
			}
			return nil
		}),
	)
	durable := &recordingApplyProvider{Provider: memory.New()}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: durable, Chain: settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassSecret: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
				OnUnavailable: settings.FailClosed, OnStale: settings.FailClosed,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := mustPrepareSet(t, key, "valid")
	for name, mutation := range map[string]settings.Mutation{
		"malformed mutation": func() settings.Mutation {
			candidate := valid
			candidate.Action = 99
			return candidate
		}(),
		"unregistered key": func() settings.Mutation {
			candidate := valid
			candidate.Key = "fleet/unregistered"
			return candidate
		}(),
		"scope outside snapshot": func() settings.Mutation {
			candidate := valid
			candidate.Scope = settings.Tenant("outside")
			return candidate
		}(),
		"codec mismatch": func() settings.Mutation {
			candidate := valid
			candidate.CodecID = "other"
			return candidate
		}(),
		"sensitivity mismatch": func() settings.Mutation {
			candidate := valid
			candidate.Sensitive = false
			return candidate
		}(),
		"invalid encoded value": func() settings.Mutation {
			candidate := valid
			candidate.Data = []byte("invalid")
			return candidate
		}(),
	} {
		_, applyErr := runtime.Apply(t.Context(), mutation)
		var committed *settings.CommittedWriteError
		if !errors.Is(applyErr, settings.ErrInvalidMutation) || errors.As(applyErr, &committed) {
			t.Errorf("%s error = %v", name, applyErr)
		}
	}
	if durable.applies != 0 {
		t.Fatalf("provider received %d invalid writes", durable.applies)
	}
	clear := valid
	clear.Action = settings.ActionClear
	clear.Data = nil
	if _, err := runtime.Apply(t.Context(), clear); err != nil {
		t.Fatalf("registered clear error = %v", err)
	}
	cleared, err := settings.ResolveCurrent(runtime, key)
	if err != nil || cleared.Status != settings.StatusCleared || durable.applies != 1 {
		t.Fatalf("registered clear result = (%+v, %v), applies = %d", cleared, err, durable.applies)
	}
}

func TestRuntimeWriteFenceOutlivesAnOlderInFlightRefresh(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "initial generation",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &sequencedFleetProvider{
		Provider: durable, blockCall: 2, entered: make(chan struct{}), release: make(chan struct{}),
		applied: make(chan struct{}),
	}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	older := make(chan error, 1)
	go func() { older <- runtime.Refresh(context.Background()) }()
	receiveFleet(t, provider.entered, "older refresh entry")

	mutation := mustPrepareSet(t, key, int64(2))
	write := make(chan error, 1)
	go func() {
		_, err := runtime.Apply(context.Background(), mutation)
		write <- err
	}()
	receiveFleet(t, provider.applied, "durable apply")
	time.Sleep(10 * time.Millisecond)
	close(provider.release)
	if err := receiveFleet(t, older, "older refresh result"); err != nil {
		t.Fatal(err)
	}
	if err := receiveFleet(t, write, "fenced write result"); err != nil {
		t.Fatal(err)
	}
	if provider.Calls() != 3 {
		t.Fatalf("bulk calls = %d, want initial, old in-flight, and fenced refresh", provider.Calls())
	}
	assertRuntimeGeneration(t, runtime, key, 2)
}

func TestRuntimeJoinedWriteAcceptsAnInFlightSnapshotThatIncludesItsCommit(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "initial generation",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &preReadBlockingProvider{
		Provider: durable, blockCall: 2, entered: make(chan struct{}), release: make(chan struct{}),
	}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	refresh := make(chan error, 1)
	go func() { refresh <- runtime.Refresh(context.Background()) }()
	receiveFleet(t, provider.entered, "pre-read refresh entry")

	write := make(chan error, 1)
	go func() {
		_, err := runtime.Apply(context.Background(), mustPrepareSet(t, key, int64(2)))
		write <- err
	}()
	deadline := time.Now().Add(time.Second)
	for provider.Applies() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(provider.release)
	if err := receiveFleet(t, refresh, "pre-read refresh result"); err != nil {
		t.Fatal(err)
	}
	if err := receiveFleet(t, write, "joined write result"); err != nil {
		t.Fatal(err)
	}
	if calls := provider.Calls(); calls != 2 {
		t.Fatalf("bulk calls = %d, want joined in-flight reconciliation", calls)
	}
	assertRuntimeGeneration(t, runtime, key, 2)
}

func TestRuntimeRejectsAnAcknowledgementThatDisagreesAtTheSameVersion(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	durable := memory.New()
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, int64(1), settings.Change{
		Actor: "operator", Reason: "initial generation",
	}); err != nil {
		t.Fatal(err)
	}
	provider := &mismatchedAcknowledgementProvider{Provider: durable}
	runtime := mustRuntime(t, provider, systemFleetClock{}, key)
	if err := runtime.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := runtime.Apply(t.Context(), mustPrepareSet(t, key, int64(2)))
	if !errors.Is(err, settings.ErrNonMonotonicSnapshot) {
		t.Fatalf("mismatched acknowledgement error = %v", err)
	}
	assertRuntimeGeneration(t, runtime, key, 1)
}

func mustPrepareSet[T any](t *testing.T, key settings.Key[T], value T) settings.Mutation {
	t.Helper()
	mutation, err := settings.PrepareSet(settings.Global(), key, value, nil, settings.Change{
		Actor: "operator", Reason: "runtime policy test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

func receiveFleet[T any](t testing.TB, channel <-chan T, operation string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
		var zero T
		return zero
	}
}

type fleetClock struct{ now time.Time }

func (clock *fleetClock) Now() time.Time { return clock.now }

type systemFleetClock struct{}

func (systemFleetClock) Now() time.Time { return time.Now() }

func mustRuntime(t testing.TB, provider settings.Provider, clock settings.RuntimeClock, definitions ...settings.Definition) *settings.Runtime {
	t.Helper()
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: provider, Chain: settings.Chain(settings.Global()), Definitions: definitions,
		Clock: clock, Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
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

type commitReportingProvider struct {
	settings.Provider
	stale settings.Record
}

type sequencedFleetProvider struct {
	settings.Provider
	mu        sync.Mutex
	calls     int
	blockCall int
	entered   chan struct{}
	release   chan struct{}
	applied   chan struct{}
}

type preReadBlockingProvider struct {
	settings.Provider
	mu        sync.Mutex
	calls     int
	applies   int
	blockCall int
	entered   chan struct{}
	release   chan struct{}
}

func (provider *preReadBlockingProvider) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	record, err := provider.Provider.Apply(ctx, mutation)
	provider.mu.Lock()
	provider.applies++
	provider.mu.Unlock()
	return record, err
}

func (provider *preReadBlockingProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call == provider.blockCall {
		close(provider.entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-provider.release:
		}
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

func (provider *preReadBlockingProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

func (provider *preReadBlockingProvider) Applies() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.applies
}

type mismatchedAcknowledgementProvider struct{ settings.Provider }

func (provider *mismatchedAcknowledgementProvider) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	record, ok, err := provider.Get(ctx, mutation.Scope, mutation.Key)
	if err != nil || !ok {
		return record, err
	}
	record.Data = append([]byte(nil), mutation.Data...)
	return record, nil
}

func (provider *sequencedFleetProvider) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	record, err := provider.Provider.Apply(ctx, mutation)
	close(provider.applied)
	return record, err
}

func (provider *sequencedFleetProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	records, err := provider.Provider.BulkGet(ctx, scopes, keys)
	if err != nil || call != provider.blockCall {
		return records, err
	}
	close(provider.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-provider.release:
		return records, nil
	}
}

func (provider *sequencedFleetProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

func (provider *commitReportingProvider) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	record, err := provider.Provider.Apply(ctx, mutation)
	if err != nil {
		return record, err
	}
	return record, &settings.CommittedWriteError{Operation: "cache fanout", Committed: true, Err: errors.New("valkey unavailable")}
}

func (provider *commitReportingProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	if provider.stale.Version != 0 {
		return []settings.Record{provider.stale}, nil
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

type blockingFleetProvider struct {
	settings.Provider
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	calls   int
	corrupt bool
}

type recordingApplyProvider struct {
	settings.Provider
	applies int
}

func (provider *recordingApplyProvider) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	provider.applies++
	return provider.Provider.Apply(ctx, mutation)
}

type failedBlockingProvider struct {
	settings.Provider
	mu      sync.Mutex
	calls   int
	failure error
	entered chan struct{}
	release chan struct{}
}

func (provider *failedBlockingProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	provider.mu.Unlock()
	if call == 1 {
		close(provider.entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-provider.release:
			return nil, provider.failure
		}
	}
	return provider.Provider.BulkGet(ctx, scopes, keys)
}

func (provider *failedBlockingProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

func (provider *failedBlockingProvider) assertCallsRemain(t *testing.T, want int, duration time.Duration) {
	t.Helper()
	time.Sleep(duration)
	if calls := provider.Calls(); calls != want {
		t.Fatalf("bulk calls = %d, want %d", calls, want)
	}
}

func (provider *blockingFleetProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	if call == 1 {
		close(provider.entered)
	}
	provider.mu.Unlock()
	if call == 1 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-provider.release:
		}
	}
	records, err := provider.Provider.BulkGet(ctx, scopes, keys)
	if err == nil && provider.corrupt {
		records[0].Data = []byte("unsafe")
	}
	return records, err
}

func (provider *blockingFleetProvider) assertCallsRemain(t *testing.T, want int, duration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		provider.mu.Lock()
		calls := provider.calls
		provider.mu.Unlock()
		if calls != want {
			t.Fatalf("bulk calls = %d, want %d while refresh is in flight", calls, want)
		}
		time.Sleep(time.Millisecond)
	}
}

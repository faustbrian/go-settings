package settings

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeWithoutPeriodicWorkOwnsNoBackgroundLoop(t *testing.T) {
	key := NewKey("internal", "idle-runtime", StringCodec{})
	runtime, err := NewRuntime(RuntimeConfig{
		Provider: internalSnapshotProvider{}, Chain: Chain(Global()), Definitions: []Definition{key},
		Provenance: ProvenanceProvider, RefreshTimeout: time.Second,
		Policies: map[SettingClass]ClassPolicy{
			ClassStandard: {
				FreshFor: time.Minute, MaxStaleness: time.Minute,
				OnUnavailable: FailClosed, OnStale: ServeLastKnownGood, OnExpired: FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	startContext, cancelStart := context.WithTimeout(t.Context(), time.Second)
	defer cancelStart()
	if err := runtime.Start(startContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.stopped:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("runtime without periodic work retained a background loop")
	}
	closeContext, cancelClose := context.WithTimeout(t.Context(), time.Second)
	defer cancelClose()
	if err := runtime.Close(closeContext); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeConstructionRejectsDefaultEncodingFailure(t *testing.T) {
	key := NewKey("internal", "bad-default", StringCodec{}, WithDefault("safe"))
	for name, definition := range map[string]Definition{
		"encoding":   failingDefaultDefinition{Definition: key},
		"validation": invalidDefaultDefinition{Definition: key},
	} {
		_, err := NewRuntime(RuntimeConfig{
			Provider: internalSnapshotProvider{}, Chain: Chain(Global()), Definitions: []Definition{definition},
			Provenance: ProvenanceProvider, RefreshTimeout: time.Second,
			Policies: map[SettingClass]ClassPolicy{
				ClassStandard: {
					FreshFor: time.Minute, MaxStaleness: time.Minute,
					OnUnavailable: UseDefault, OnStale: UseDefault, OnExpired: UseDefault,
				},
			},
		})
		if !errors.Is(err, ErrInvalidDefinition) {
			t.Errorf("%s runtime construction error = %v", name, err)
		}
	}
}

func TestRuntimeClassifiesEveryInvalidationBoundary(t *testing.T) {
	runtime := &Runtime{}
	valid := Invalidation{
		ProtocolVersion: InvalidationProtocolVersion,
		Scope:           Global(), Key: "fleet/key", Version: 2, State: StateValue,
	}
	malformed := []Invalidation{
		func() Invalidation { event := valid; event.ProtocolVersion++; return event }(),
		func() Invalidation { event := valid; event.Version = 0; return event }(),
		func() Invalidation { event := valid; event.Key = ""; return event }(),
		func() Invalidation { event := valid; event.Scope = Tenant(""); return event }(),
		func() Invalidation { event := valid; event.State = State(255); return event }(),
	}
	for index, event := range malformed {
		if !runtime.acceptInvalidation(event, map[snapshotCoordinate]uint64{}) {
			t.Errorf("malformed invalidation %d was dropped instead of reconciled", index)
		}
	}
	for _, state := range []State{StateMissing, StateValue, StateCleared} {
		event := valid
		event.State = state
		watermarks := map[snapshotCoordinate]uint64{}
		if !runtime.acceptInvalidation(event, watermarks) {
			t.Errorf("valid state %d did not request reconciliation", state)
		}
		if runtime.acceptInvalidation(event, watermarks) {
			t.Errorf("duplicate state %d invalidation was not dropped", state)
		}
		event.Version--
		if runtime.acceptInvalidation(event, watermarks) {
			t.Errorf("reordered state %d invalidation was not dropped", state)
		}
	}
}

func TestRuntimeJitterClampsToInclusiveConfiguredBounds(t *testing.T) {
	const (
		base    = 20 * time.Millisecond
		maximum = 10 * time.Millisecond
	)
	for _, test := range []struct {
		name   string
		jitter time.Duration
		want   time.Duration
	}{
		{name: "negative", jitter: -1, want: base},
		{name: "zero", jitter: 0, want: base},
		{name: "maximum", jitter: maximum, want: base + maximum},
		{name: "excessive", jitter: maximum + 1, want: base + maximum},
	} {
		runtime := &Runtime{maxJitter: maximum, jitter: func(time.Duration) time.Duration { return test.jitter }}
		if got := runtime.withJitter(base); got != test.want {
			t.Errorf("%s jitter = %v, want %v", test.name, got, test.want)
		}
	}
	if got := (&Runtime{}).withJitter(base); got != base {
		t.Fatalf("disabled jitter = %v, want %v", got, base)
	}
}

type failingDefaultDefinition struct{ Definition }

func (failingDefaultDefinition) DefaultEncoded() ([]byte, bool, error) {
	return nil, true, errors.New("encode default")
}

type invalidDefaultDefinition struct{ Definition }

func (invalidDefaultDefinition) ValidateEncoded([]byte) error {
	return errors.New("invalid encoded default")
}

type internalSnapshotProvider struct{}

func (internalSnapshotProvider) Capabilities() Capabilities {
	return Capabilities{Snapshots: true}
}
func (internalSnapshotProvider) Get(context.Context, Scope, string) (Record, bool, error) {
	return Record{}, false, nil
}
func (internalSnapshotProvider) BulkGet(context.Context, []Scope, []string) ([]Record, error) {
	return nil, nil
}
func (internalSnapshotProvider) Apply(context.Context, Mutation) (Record, error) {
	return Record{}, ErrUnsupported
}
func (internalSnapshotProvider) BulkApply(context.Context, []Mutation) ([]Record, error) {
	return nil, ErrUnsupported
}
func (internalSnapshotProvider) History(context.Context, HistoryQuery) ([]ChangeRecord, error) {
	return nil, ErrUnsupported
}

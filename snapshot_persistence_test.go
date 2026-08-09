package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestSnapshotCacheRoundTripPreservesValidatedOriginAndAge(t *testing.T) {
	t.Parallel()

	capturedAt := time.Unix(1_800_000_000, 0).UTC()
	key := settings.NewKey("fleet", "mode", settings.StringCodec{},
		settings.WithValidation(func(value string) error {
			if value != "safe" {
				return errors.New("unsafe")
			}
			return nil
		}),
	)
	durable := memory.NewWithClock(func() time.Time { return capturedAt })
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "cache snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Set(t.Context(), durable, settings.Tenant("acme"), key, "safe", settings.Change{
		Actor: "operator", Reason: "tenant snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	chain := settings.Chain(settings.Tenant("acme"), settings.Global())
	snapshot, err := settings.CaptureWithOptions(t.Context(), durable,
		chain, settings.CaptureOptions{
			CapturedAt: capturedAt, Provenance: settings.ProvenancePostgreSQL,
		}, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := settings.RestoreSnapshot(encoded, chain, key)
	if err != nil {
		t.Fatal(err)
	}
	metadata := restored.Metadata(capturedAt.Add(time.Minute))
	if metadata.Provenance != settings.ProvenanceSnapshotCache ||
		metadata.Origin != settings.ProvenancePostgreSQL || metadata.Age != time.Minute ||
		metadata.Revision != snapshot.Version() {
		t.Fatalf("restored metadata = %+v", metadata)
	}
	result, err := settings.ResolveSnapshot(restored, key, chain)
	if err != nil || result.Value != "safe" {
		t.Fatalf("restored result = (%+v, %v)", result, err)
	}

	tampered := bytes.Replace(encoded, []byte("c2FmZQ=="), []byte("ZXZpbA=="), 1)
	if _, err := settings.RestoreSnapshot(tampered, chain, key); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("tampered snapshot error = %v", err)
	}
	if _, err := settings.RestoreSnapshot(make([]byte, 2<<20+1), chain, key); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("oversized snapshot error = %v", err)
	}
}

func TestSnapshotCacheRejectsMalformedEnvelopeContracts(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.NewWithClock(func() time.Time { return now })
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "snapshot envelope",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := settings.CaptureWithOptions(t.Context(), durable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now, Provenance: settings.ProvenancePostgreSQL,
		}, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(encoded, &base); err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"schema", func(wire map[string]any) { wire["schema"] = 2 }},
		{"revision", func(wire map[string]any) { wire["revision"] = "" }},
		{"captured at", func(wire map[string]any) { wire["captured_at"] = "0001-01-01T00:00:00Z" }},
		{"unknown origin", func(wire map[string]any) { wire["origin"] = "unknown" }},
		{"default origin", func(wire map[string]any) { wire["origin"] = string(settings.ProvenanceDefaults) }},
		{"cache origin", func(wire map[string]any) { wire["origin"] = string(settings.ProvenanceSnapshotCache) }},
		{"digest", func(wire map[string]any) { wire["revision"] = strings.Repeat("0", 64) }},
		{"unknown field", func(wire map[string]any) { wire["unexpected"] = true }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			wire := cloneJSONMap(t, base)
			test.mutate(wire)
			data, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := settings.RestoreSnapshot(data, settings.Chain(settings.Global()), key); !errors.Is(err, settings.ErrInvalidValue) {
				t.Fatalf("invalid envelope error = %v", err)
			}
		})
	}
	if _, err := settings.RestoreSnapshot(encoded, settings.ResolutionChain{}, key); !errors.Is(err, settings.ErrInvalidChain) {
		t.Fatalf("invalid restore chain error = %v", err)
	}
	if _, err := (settings.Snapshot{}).MarshalBinary(); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("empty marshal error = %v", err)
	}

	largeDefinitions := make([]settings.Definition, 0, 3)
	largeDurable := memory.NewWithClock(func() time.Time { return now })
	for index := 0; index < 3; index++ {
		large := settings.NewKey("fleet", "large-"+string(rune('a'+index)), settings.StringCodec{})
		largeDefinitions = append(largeDefinitions, large)
		if _, err := settings.Set(t.Context(), largeDurable, settings.Global(), large,
			strings.Repeat("x", 800_000), settings.Change{Actor: "operator", Reason: "size bound"}); err != nil {
			t.Fatal(err)
		}
	}
	largeSnapshot, err := settings.CaptureWithOptions(t.Context(), largeDurable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now, Provenance: settings.ProvenancePostgreSQL,
		}, largeDefinitions...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := largeSnapshot.MarshalBinary(); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("oversized marshal error = %v", err)
	}
	if metadata := snapshot.Metadata(now.Add(-time.Second)); metadata.Age != 0 {
		t.Fatalf("clock rollback age = %v", metadata.Age)
	}
}

func cloneJSONMap(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func TestRuntimeStartupUsesOnlyPolicyCompliantCachedState(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_100, 0).UTC()
	clock := &fleetClock{now: now}
	key := settings.NewKey("fleet", "theme", settings.StringCodec{}, settings.WithDefault("system"))
	durable := memory.NewWithClock(func() time.Time { return now.Add(-3 * time.Second) })
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "dark", settings.Change{
		Actor: "operator", Reason: "cache startup state",
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := settings.CaptureWithOptions(t.Context(), durable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now.Add(-3 * time.Second), Provenance: settings.ProvenancePostgreSQL,
		}, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	store := &fleetSnapshotStore{data: encoded, present: true}
	runtime, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: unavailableFleetProvider{Provider: durable},
		Chain:    settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Clock: clock, Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		SnapshotStore: store,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
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
	result, err := settings.ResolveCurrent(runtime, key)
	if err != nil || result.Value != "dark" || result.Freshness != settings.Stale ||
		result.Snapshot.Provenance != settings.ProvenanceSnapshotCache {
		t.Fatalf("cached startup result = (%+v, %v)", result, err)
	}
	if err := runtime.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	store.data = []byte("malformed")
	blocked, err := settings.NewRuntime(settings.RuntimeConfig{
		Provider: unavailableFleetProvider{Provider: durable},
		Chain:    settings.Chain(settings.Global()), Definitions: []settings.Definition{key},
		Clock: clock, Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: time.Second,
		SnapshotStore: store,
		Policies: map[settings.SettingClass]settings.ClassPolicy{
			settings.ClassStandard: {
				FreshFor: time.Second, MaxStaleness: 5 * time.Second,
				OnUnavailable: settings.FailClosed, OnStale: settings.FailClosed,
				OnExpired: settings.FailClosed,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := blocked.Start(t.Context()); err == nil || blocked.Ready() {
		t.Fatalf("malformed cache startup = (ready %v, %v)", blocked.Ready(), err)
	}
}

type fleetSnapshotStore struct {
	data    []byte
	present bool
}

func (store *fleetSnapshotStore) Load(context.Context) ([]byte, bool, error) {
	return append([]byte(nil), store.data...), store.present, nil
}

func (store *fleetSnapshotStore) Save(context.Context, []byte) error { return nil }

type unavailableFleetProvider struct{ settings.Provider }

func (unavailableFleetProvider) BulkGet(context.Context, []settings.Scope, []string) ([]settings.Record, error) {
	return nil, errors.New("postgres unavailable")
}

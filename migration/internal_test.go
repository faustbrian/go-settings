package migration

import (
	"context"
	"errors"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

type testStringCodec struct{ version uint32 }

func (codec testStringCodec) ID() string                    { return "test-string" }
func (codec testStringCodec) Version() uint32               { return codec.version }
func (testStringCodec) Encode(value string) ([]byte, error) { return []byte(value), nil }
func (testStringCodec) Decode(data []byte) (string, error)  { return string(data), nil }

type faultProvider struct {
	settings.Provider
	getCalls int
	getError int
	applyErr error
	bulkErr  error
}

func (provider *faultProvider) Get(ctx context.Context, scope settings.Scope, key string) (settings.Record, bool, error) {
	provider.getCalls++
	if provider.getCalls == provider.getError {
		return settings.Record{}, false, errors.New("get")
	}
	return provider.Provider.Get(ctx, scope, key)
}
func (provider *faultProvider) Apply(context.Context, settings.Mutation) (settings.Record, error) {
	return settings.Record{}, provider.applyErr
}
func (provider *faultProvider) BulkApply(context.Context, []settings.Mutation) ([]settings.Record, error) {
	return nil, provider.bulkErr
}

func TestInternalMigrationFailureBranches(t *testing.T) {
	ctx := context.Background()
	change := settings.Change{Actor: "migration", Reason: "test"}
	scope := settings.Global()
	old := settings.NewKey("internal", "old", testStringCodec{version: 1})
	to := settings.NewKey("internal", "new", testStringCodec{version: 1})
	upgraded := settings.NewKey("internal", "old", testStringCodec{version: 2})

	invalidDefault := settings.NewKey("internal", "integer", settings.IntCodec{})
	for _, step := range []Step{
		ChangeDefault("bad-old", invalidDefault, []byte("bad"), []byte("1")),
		ChangeDefault("bad-new", invalidDefault, []byte("1"), []byte("bad")),
		{id: "unknown", kind: "unknown", to: to},
	} {
		plan := Plan{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []Step{step}}
		if validatePlan(plan, []settings.Scope{scope}) == nil {
			t.Fatalf("invalid step accepted: %#v", step)
		}
	}
	if err := applyStep(ctx, memory.New(), scope, Step{kind: "unknown"}, change); err == nil {
		t.Fatal("unknown apply step accepted")
	}

	base := memory.New()
	if err := applyRename(ctx, &faultProvider{Provider: base, getError: 1}, scope, Rename("rename", old, to), change); err == nil {
		t.Fatal("source read error hidden")
	}
	if err := applyRename(ctx, &faultProvider{Provider: base, getError: 2}, scope, Rename("rename", old, to), change); err == nil {
		t.Fatal("target read error hidden")
	}
	if err := applyRename(ctx, base, scope, Rename("rename", old, to), change); err != nil {
		t.Fatalf("missing source rename: %v", err)
	}
	_, _ = base.Apply(ctx, settings.Mutation{
		Scope: scope, Key: old.StableID(), Action: settings.ActionSet, Data: []byte("value"),
		CodecID: "wrong", CodecVersion: 1, Change: change,
	})
	if err := applyRename(ctx, base, scope, Rename("rename", old, to), change); err == nil {
		t.Fatal("source codec mismatch accepted")
	}

	codecChangeStore := memory.New()
	_, _ = settings.Set(ctx, codecChangeStore, scope, old, "value", change)
	changedTarget := settings.NewKey("internal", "new", testStringCodec{version: 2})
	if err := applyRename(ctx, codecChangeStore, scope, Rename("rename", old, changedTarget), change); err == nil {
		t.Fatal("rename changed codec")
	}
	invalidTarget := settings.NewKey("internal", "new", testStringCodec{version: 1},
		settings.WithValidation(func(string) error { return errors.New("invalid") }))
	if err := applyRename(ctx, codecChangeStore, scope, Rename("rename", old, invalidTarget), change); err == nil {
		t.Fatal("rename accepted invalid target value")
	}
	if err := applyRename(ctx, &faultProvider{Provider: codecChangeStore, bulkErr: errors.New("bulk")},
		scope, Rename("rename", old, to), change); err == nil {
		t.Fatal("rename bulk error hidden")
	}

	transform := Transform("transform", old, upgraded, func(data []byte) ([]byte, error) { return data, nil })
	if err := applyTransform(ctx, &faultProvider{Provider: memory.New(), getError: 1}, scope, transform, change); err == nil {
		t.Fatal("transform get error hidden")
	}
	if err := applyTransform(ctx, memory.New(), scope, transform, change); err != nil {
		t.Fatalf("missing transform: %v", err)
	}
	mismatch := memory.New()
	_, _ = mismatch.Apply(ctx, settings.Mutation{
		Scope: scope, Key: old.StableID(), Action: settings.ActionSet, Data: []byte("value"),
		CodecID: "wrong", CodecVersion: 1, Change: change,
	})
	if err := applyTransform(ctx, mismatch, scope, transform, change); err == nil {
		t.Fatal("transform source mismatch accepted")
	}
	invalidTransformTarget := settings.NewKey("internal", "old", testStringCodec{version: 2},
		settings.WithValidation(func(string) error { return errors.New("invalid") }))
	if err := applyTransform(ctx, codecChangeStore, scope,
		Transform("transform", old, invalidTransformTarget, func(data []byte) ([]byte, error) { return data, nil }), change); err == nil {
		t.Fatal("transform accepted invalid output")
	}
	if err := applyTransform(ctx, &faultProvider{Provider: codecChangeStore, applyErr: errors.New("apply")},
		scope, transform, change); err == nil {
		t.Fatal("transform apply error hidden")
	}
}

func TestRunReportsSkippedCheckpoints(t *testing.T) {
	key := settings.NewKey("internal", "value", settings.StringCodec{})
	plan := Plan{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []Step{
		ChangeDefault("default", key, []byte("old"), []byte("new")),
	}}
	journal := NewMemoryJournal()
	change := settings.Change{Actor: "migration", Reason: "test"}
	if _, err := Run(t.Context(), memory.New(), journal, plan, []settings.Scope{settings.Global()}, change); err != nil {
		t.Fatal(err)
	}
	report, err := Run(t.Context(), memory.New(), journal, plan, []settings.Scope{settings.Global()}, change)
	if err != nil || report.Skipped != 1 {
		t.Fatalf("skipped report = %#v, %v", report, err)
	}
}

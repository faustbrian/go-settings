package migration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	"github.com/faustbrian/golib/pkg/settings/migration"
)

type faultJournal struct{ readErr, writeErr error }

func (journal faultJournal) Completed(context.Context, string, string, settings.Scope) (bool, error) {
	return false, journal.readErr
}
func (journal faultJournal) MarkCompleted(context.Context, string, string, settings.Scope, time.Time) error {
	return journal.writeErr
}

type nonAtomic struct{ settings.Provider }

func (provider nonAtomic) Capabilities() settings.Capabilities {
	capabilities := provider.Provider.Capabilities()
	capabilities.AtomicBulk = false
	return capabilities
}

func TestPlanValidationAndJournalFailures(t *testing.T) {
	t.Parallel()

	key := settings.NewKey("migration", "value", settings.StringCodec{})
	other := settings.NewKey("migration", "other", settings.StringCodec{})
	validStep := migration.Rename("rename", key, other)
	if validStep.ID() != "rename" || validStep.Kind() != migration.KindRename {
		t.Fatalf("step identity = %s, %s", validStep.ID(), validStep.Kind())
	}
	change := settings.Change{Actor: "migration", Reason: "test"}
	validPlan := migration.Plan{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{validStep}}
	invalidPlans := []migration.Plan{
		{},
		{ID: "plan", FromSchema: "v1", ToSchema: "v1", Steps: []migration.Step{validStep}},
		{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{{}}},
		{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{validStep, validStep}},
		{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{migration.Rename("same", key, key)}},
		{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{migration.Transform("bad", key, key, nil)}},
		{ID: "plan", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{migration.Transform("bad", key, other, func(data []byte) ([]byte, error) { return data, nil })}},
	}
	for index, plan := range invalidPlans {
		if _, err := migration.Run(t.Context(), memory.New(), migration.NewMemoryJournal(), plan,
			[]settings.Scope{settings.Global()}, change); err == nil {
			t.Fatalf("invalid plan %d accepted", index)
		}
	}
	if _, err := migration.Run(t.Context(), memory.New(), migration.NewMemoryJournal(), validPlan, nil, change); err == nil {
		t.Fatal("empty scopes accepted")
	}
	if _, err := migration.Run(t.Context(), memory.New(), migration.NewMemoryJournal(), validPlan,
		[]settings.Scope{settings.Tenant("")}, change); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if _, err := migration.Run(t.Context(), memory.New(), faultJournal{readErr: errors.New("read")}, validPlan,
		[]settings.Scope{settings.Global()}, change); err == nil {
		t.Fatal("journal read error hidden")
	}
	if _, err := migration.Run(t.Context(), memory.New(), faultJournal{writeErr: errors.New("write")}, validPlan,
		[]settings.Scope{settings.Global()}, change); err == nil {
		t.Fatal("journal write error hidden")
	}
}

func TestRenameAndTransformFailureAndClearedContracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	change := settings.Change{Actor: "migration", Reason: "test"}
	oldKey := settings.NewKey("migration", "old", versionedStringCodec{version: 1})
	newKey := settings.NewKey("migration", "new", versionedStringCodec{version: 1})
	upgraded := settings.NewKey("migration", "new", versionedStringCodec{version: 2})
	renamePlan := migration.Plan{ID: "rename", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{
		migration.Rename("rename", oldKey, newKey),
	}}
	if _, err := migration.Run(ctx, nonAtomic{memory.New()}, migration.NewMemoryJournal(), renamePlan,
		[]settings.Scope{settings.Global()}, change); !errors.Is(err, settings.ErrUnsupported) {
		t.Fatalf("non-atomic rename = %v", err)
	}

	conflict := memory.New()
	_, _ = settings.Set(ctx, conflict, settings.Global(), oldKey, "old", change)
	_, _ = settings.Set(ctx, conflict, settings.Global(), newKey, "new", change)
	if _, err := migration.Run(ctx, conflict, migration.NewMemoryJournal(), renamePlan,
		[]settings.Scope{settings.Global()}, change); err == nil {
		t.Fatal("rename overwrote existing target")
	}

	cleared := memory.New()
	_, _ = settings.Clear(ctx, cleared, settings.Global(), oldKey, change)
	if _, err := migration.Run(ctx, cleared, migration.NewMemoryJournal(), renamePlan,
		[]settings.Scope{settings.Global()}, change); err != nil {
		t.Fatal(err)
	}
	record, ok, _ := cleared.Get(ctx, settings.Global(), newKey.StableID())
	if !ok || record.State != settings.StateCleared {
		t.Fatalf("renamed cleared record = %#v, %v", record, ok)
	}

	transformPlan := migration.Plan{ID: "transform", FromSchema: "v1", ToSchema: "v2", Steps: []migration.Step{
		migration.Transform("upgrade", newKey, upgraded, func([]byte) ([]byte, error) { return nil, errors.New("transform") }),
	}}
	values := memory.New()
	_, _ = settings.Set(ctx, values, settings.Global(), newKey, "value", change)
	if _, err := migration.Run(ctx, values, migration.NewMemoryJournal(), transformPlan,
		[]settings.Scope{settings.Global()}, change); err == nil {
		t.Fatal("transform error hidden")
	}

	clearedTransform := memory.New()
	_, _ = settings.Clear(ctx, clearedTransform, settings.Global(), newKey, change)
	transformPlan.Steps = []migration.Step{migration.Transform("upgrade", newKey, upgraded,
		func(data []byte) ([]byte, error) { return data, nil })}
	if _, err := migration.Run(ctx, clearedTransform, migration.NewMemoryJournal(), transformPlan,
		[]settings.Scope{settings.Global()}, change); err != nil {
		t.Fatal(err)
	}
	record, ok, _ = clearedTransform.Get(ctx, settings.Global(), upgraded.StableID())
	if !ok || record.State != settings.StateCleared || record.CodecVersion != 2 {
		t.Fatalf("transformed cleared record = %#v, %v", record, ok)
	}
}

package settings_test

import (
	"context"
	"errors"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestNamespaceAndDisplayNamesDoNotChangeStableIdentifiers(t *testing.T) {
	t.Parallel()

	registry := settings.NewRegistry()
	namespace := settings.NewNamespace("billing", "Billing preferences")
	if err := registry.RegisterNamespace(namespace); err != nil {
		t.Fatalf("register namespace: %v", err)
	}
	if err := registry.RegisterNamespace(namespace); !errors.Is(err, settings.ErrDuplicateDefinition) {
		t.Fatalf("duplicate namespace error = %v", err)
	}
	key := settings.NewKey("billing", "invoice.due_days", settings.IntCodec{},
		settings.WithDisplayName[int64]("Invoice payment period"),
	)
	if key.StableID() != "billing/invoice.due_days" || key.DisplayName() != "Invoice payment period" {
		t.Fatalf("key identity = %q, display = %q", key.StableID(), key.DisplayName())
	}
}

func TestPreparedBulkSupportsHeterogeneousAtomicMutations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	scope := settings.Tenant("acme")
	change := settings.Change{Actor: "operator", Reason: "bulk update"}
	enabled := settings.NewKey("ui", "enabled", settings.BoolCodec{})
	label := settings.NewKey("ui", "label", settings.StringCodec{})
	first, err := settings.PrepareSet(scope, enabled, true, nil, change)
	if err != nil {
		t.Fatalf("prepare bool: %v", err)
	}
	second, err := settings.PrepareSet(scope, label, "Acme", nil, change)
	if err != nil {
		t.Fatalf("prepare string: %v", err)
	}
	records, err := settings.Bulk(ctx, provider, []settings.Mutation{first, second}, settings.RequireAtomic)
	if err != nil || len(records) != 2 {
		t.Fatalf("bulk = %#v, %v", records, err)
	}
	expected := records[0].Version
	if _, err := settings.CompareAndClear(ctx, provider, scope, enabled, expected+1, change); !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("stale clear error = %v, want conflict", err)
	}
}

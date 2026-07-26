package settings_test

import (
	"context"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestResolutionDistinguishesStoredClearedInheritedDefaultedAndMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	key := settings.NewKey("billing", "invoice.due_days", settings.IntCodec{},
		settings.WithDefault[int64](14),
	)
	global := settings.Global()
	tenant := settings.Tenant("acme")
	user := settings.User("alice")
	chain := settings.Chain(user, tenant, global)
	change := settings.Change{Actor: "operator:1", Reason: "test"}

	if _, err := settings.Set(ctx, provider, global, key, 30, change); err != nil {
		t.Fatalf("set global: %v", err)
	}
	if _, err := settings.Clear(ctx, provider, tenant, key, change); err != nil {
		t.Fatalf("clear tenant: %v", err)
	}

	cleared, err := settings.Resolve(ctx, provider, key, chain)
	if err != nil {
		t.Fatalf("resolve cleared: %v", err)
	}
	if cleared.Status != settings.StatusCleared || cleared.Owner != tenant {
		t.Fatalf("cleared result = %#v", cleared)
	}

	if _, err := settings.Set(ctx, provider, user, key, 0, change); err != nil {
		t.Fatalf("set explicit zero: %v", err)
	}
	stored, err := settings.Resolve(ctx, provider, key, chain)
	if err != nil {
		t.Fatalf("resolve stored: %v", err)
	}
	if stored.Status != settings.StatusStored || stored.Value != 0 || stored.Owner != user {
		t.Fatalf("stored result = %#v", stored)
	}

	if _, err := settings.Inherit(ctx, provider, user, key, change); err != nil {
		t.Fatalf("inherit user: %v", err)
	}
	if _, err := settings.Inherit(ctx, provider, tenant, key, change); err != nil {
		t.Fatalf("inherit tenant: %v", err)
	}
	inherited, err := settings.Resolve(ctx, provider, key, chain)
	if err != nil {
		t.Fatalf("resolve inherited: %v", err)
	}
	if inherited.Status != settings.StatusInherited || inherited.Value != 30 ||
		inherited.Owner != global || len(inherited.Path) != 3 {
		t.Fatalf("inherited result = %#v", inherited)
	}

	if _, err := settings.Inherit(ctx, provider, global, key, change); err != nil {
		t.Fatalf("inherit global: %v", err)
	}
	defaulted, err := settings.Resolve(ctx, provider, key, chain)
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if defaulted.Status != settings.StatusDefaulted || defaulted.Value != 14 {
		t.Fatalf("defaulted result = %#v", defaulted)
	}

	withoutDefault := settings.NewKey("billing", "invoice.prefix", settings.StringCodec{})
	missing, err := settings.Resolve(ctx, provider, withoutDefault, chain)
	if err != nil {
		t.Fatalf("resolve missing: %v", err)
	}
	if missing.Status != settings.StatusMissing {
		t.Fatalf("missing result = %#v", missing)
	}
}

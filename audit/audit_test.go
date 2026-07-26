package audit_test

import (
	"context"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/audit"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestReadRedactsSensitiveHistoryAtThePublicBoundary(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	registry := settings.NewRegistry()
	key := settings.NewKey("integration", "token", settings.StringCodec{}, settings.WithSensitive[string]())
	if err := registry.Register(key); err != nil {
		t.Fatalf("register: %v", err)
	}
	scope := settings.Tenant("acme")
	if _, err := settings.Set(ctx, provider, scope, key, "secret", settings.Change{
		Actor: "operator", Reason: "rotation",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	records, err := audit.Read(ctx, provider, registry, settings.HistoryQuery{
		Scope: scope, Key: key.StableID(), Limit: 10,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(records) != 1 || !records[0].After.Redacted || len(records[0].After.Data) != 0 {
		t.Fatalf("audit records = %#v", records)
	}
}

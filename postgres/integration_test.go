package postgres_test

import (
	"context"
	"os"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/postgres"
	"github.com/faustbrian/golib/pkg/settings/settingstest"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProviderConformance(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL is not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	store := postgres.New(pool)
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	settingstest.RunProvider(t, func(t *testing.T) settings.Provider {
		if _, err := pool.Exec(t.Context(), "TRUNCATE settings_history, settings_values, settings_migrations"); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		return store
	})

	if capabilities := store.Capabilities(); !capabilities.AtomicBulk || !capabilities.Snapshots {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	key := settings.NewKey("integration", "snapshot", settings.StringCodec{})
	change := settings.Change{Actor: "integration", Reason: "snapshot"}
	if _, err := settings.Set(t.Context(), store, settings.Global(), key, "value", change); err != nil {
		t.Fatalf("set snapshot value: %v", err)
	}
	snapshot, err := settings.Capture(t.Context(), store, settings.Chain(settings.Global()), key)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	result, err := settings.ResolveSnapshot(snapshot, key, settings.Chain(settings.Global()))
	if err != nil || result.Value != "value" {
		t.Fatalf("snapshot result = %#v, %v", result, err)
	}
	if _, err := settings.Clear(t.Context(), store, settings.Global(), key, change); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := settings.Inherit(t.Context(), store, settings.Global(), key, change); err != nil {
		t.Fatalf("inherit: %v", err)
	}
	completed, err := store.Completed(t.Context(), "plan", "step", settings.Global())
	if err != nil || completed {
		t.Fatalf("initial checkpoint = %v, %v", completed, err)
	}
	if err := store.MarkCompleted(t.Context(), "plan", "step", settings.Global(), change.At); err != nil {
		t.Fatalf("mark checkpoint: %v", err)
	}
	completed, err = store.Completed(t.Context(), "plan", "step", settings.Global())
	if err != nil || !completed {
		t.Fatalf("stored checkpoint = %v, %v", completed, err)
	}
}

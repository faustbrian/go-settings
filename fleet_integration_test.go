package settings_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/postgres"
	cache "github.com/faustbrian/golib/pkg/settings/valkey"
	"github.com/jackc/pgx/v5/pgxpool"
	valkeygo "github.com/valkey-io/valkey-go"
)

func TestRuntimeFleetWithRealPostgreSQLAndValkey(t *testing.T) {
	postgresURL := os.Getenv("POSTGRES_URL")
	valkeyAddress := os.Getenv("VALKEY_ADDR")
	if postgresURL == "" || valkeyAddress == "" {
		t.Skip("POSTGRES_URL and VALKEY_ADDR are required")
	}

	pool, err := pgxpool.New(t.Context(), postgresURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	durable := postgres.New(pool)
	if err := durable.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	client, err := valkeygo.NewClient(valkeygo.ClientOption{InitAddress: []string{valkeyAddress}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	prefix := "settings:fleet-integration:" + time.Now().UTC().Format("20060102150405.000000000")
	newProvider := func() *cache.Cache {
		return cache.New(durable, cache.NewNativeTransport(client), cache.Config{
			Prefix: prefix, TTL: time.Minute, ReadPolicy: cache.BoundedStale, OutagePolicy: cache.Bypass,
		})
	}
	key := settings.NewKey("integration", "fleet-runtime", settings.StringCodec{})
	scope := settings.Tenant(prefix)
	if _, err := settings.Set(t.Context(), durable, scope, key, "before", settings.Change{
		Actor: "integration", Reason: "seed fleet runtime",
	}); err != nil {
		t.Fatal(err)
	}
	chain := settings.Chain(scope)
	newRuntime := func(provider *cache.Cache) *settings.Runtime {
		runtime, runtimeErr := settings.NewRuntime(settings.RuntimeConfig{
			Provider: provider, Chain: chain, Definitions: []settings.Definition{key},
			Provenance: settings.ProvenancePostgreSQL, RefreshTimeout: 5 * time.Second,
			RefreshInterval: 5 * time.Second,
			Invalidations:   provider, WatchBuffer: 8, ReconnectDelay: 50 * time.Millisecond,
			InvalidationDebounce: 10 * time.Millisecond,
			Policies: map[settings.SettingClass]settings.ClassPolicy{
				settings.ClassStandard: {
					FreshFor: time.Minute, MaxStaleness: time.Minute,
					OnUnavailable: settings.FailClosed, OnStale: settings.ServeLastKnownGood,
					OnExpired: settings.FailClosed,
				},
			},
		})
		if runtimeErr != nil {
			t.Fatal(runtimeErr)
		}
		return runtime
	}
	writer := newRuntime(newProvider())
	reader := newRuntime(newProvider())
	for _, runtime := range []*settings.Runtime{writer, reader} {
		if err := runtime.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, runtime := range []*settings.Runtime{writer, reader} {
			if err := runtime.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}
	})
	time.Sleep(100 * time.Millisecond)

	mutation, err := settings.PrepareSet(scope, key, "after", nil, settings.Change{
		Actor: "integration", Reason: "prove cross-pod convergence",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Apply(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, resolveErr := settings.ResolveCurrent(reader, key)
		if resolveErr == nil && result.Value == "after" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, resolveErr := settings.ResolveCurrent(reader, key)
	t.Fatalf("reader did not converge through Valkey invalidation: value=%q err=%v", result.Value, resolveErr)
}

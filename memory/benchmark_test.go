package memory_test

import (
	"context"
	"fmt"
	"testing"

	settings "github.com/faustbrian/go-settings/v3"
	"github.com/faustbrian/go-settings/v3/memory"
)

// BenchmarkHotRead measures copied payload reads of one seeded global value; seeding is
// excluded and errors are not separately validated.
func BenchmarkHotRead(b *testing.B) {
	provider, key := seededProvider(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = provider.Get(context.Background(), settings.Global(), key.StableID())
	}
}

// BenchmarkColdRead measures three-owner misses followed by a declared default, without
// external IO; compare with equivalent chain depth/input shape.
func BenchmarkColdRead(b *testing.B) {
	provider := memory.New()
	key := settings.NewKey("benchmark", "missing", settings.StringCodec{}, settings.WithDefault("default"))
	chain := settings.Chain(settings.User("user"), settings.Tenant("tenant"), settings.Global())
	b.ReportAllocs()
	for b.Loop() {
		_, _ = settings.Resolve(context.Background(), provider, key, chain)
	}
}

// BenchmarkResolutionDepth measures four-owner resolution ending at a seeded global value,
// including payload copy and decode; setup is outside the Loop body.
func BenchmarkResolutionDepth(b *testing.B) {
	provider, key := seededProvider(b)
	chain := settings.Chain(settings.User("user"), settings.Resource("resource"), settings.Tenant("tenant"), settings.Global())
	b.ReportAllocs()
	for b.Loop() {
		_, _ = settings.Resolve(context.Background(), provider, key, chain)
	}
}

// BenchmarkBulkRead100 measures a coherent copied read of 100 global values; seeding is
// excluded and no external backend is involved.
func BenchmarkBulkRead100(b *testing.B) {
	provider := memory.New()
	keys := make([]string, 100)
	change := settings.Change{Actor: "benchmark", Reason: "seed"}
	for index := range keys {
		key := settings.NewKey("benchmark", fmt.Sprintf("key-%d", index), settings.StringCodec{})
		keys[index] = key.StableID()
		_, _ = settings.Set(context.Background(), provider, settings.Global(), key, "value", change)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = provider.BulkGet(context.Background(), []settings.Scope{settings.Global()}, keys)
	}
}

// BenchmarkProviderContention measures parallel audited writes to one memory coordinate,
// including lock contention and growing retained history; it is not a bounded steady-state
// backend comparison.
func BenchmarkProviderContention(b *testing.B) {
	provider, key := seededProvider(b)
	change := settings.Change{Actor: "benchmark", Reason: "contention"}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = settings.Set(context.Background(), provider, settings.Global(), key, "value", change)
		}
	})
}

func seededProvider(b *testing.B) (*memory.Store, settings.Key[string]) {
	b.Helper()
	provider := memory.New()
	key := settings.NewKey("benchmark", "value", settings.StringCodec{})
	_, err := settings.Set(context.Background(), provider, settings.Global(), key, "value",
		settings.Change{Actor: "benchmark", Reason: "seed"})
	if err != nil {
		b.Fatal(err)
	}
	return provider, key
}

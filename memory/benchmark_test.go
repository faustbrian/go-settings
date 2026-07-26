package memory_test

import (
	"context"
	"fmt"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func BenchmarkHotRead(b *testing.B) {
	provider, key := seededProvider(b)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _ = provider.Get(context.Background(), settings.Global(), key.StableID())
	}
}

func BenchmarkColdRead(b *testing.B) {
	provider := memory.New()
	key := settings.NewKey("benchmark", "missing", settings.StringCodec{}, settings.WithDefault("default"))
	chain := settings.Chain(settings.User("user"), settings.Tenant("tenant"), settings.Global())
	b.ReportAllocs()
	for b.Loop() {
		_, _ = settings.Resolve(context.Background(), provider, key, chain)
	}
}

func BenchmarkResolutionDepth(b *testing.B) {
	provider, key := seededProvider(b)
	chain := settings.Chain(settings.User("user"), settings.Resource("resource"), settings.Tenant("tenant"), settings.Global())
	b.ReportAllocs()
	for b.Loop() {
		_, _ = settings.Resolve(context.Background(), provider, key, chain)
	}
}

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

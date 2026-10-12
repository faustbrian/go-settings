package valkey_test

import (
	"context"
	"testing"
	"time"

	settings "github.com/faustbrian/go-settings/v3"
	"github.com/faustbrian/go-settings/v3/memory"
	cache "github.com/faustbrian/go-settings/v3/valkey"
)

// BenchmarkCacheInvalidation measures memory-backed writes, cache serialization and
// invalidation through a fake transport; it excludes network latency and retains growing audit
// history.
func BenchmarkCacheInvalidation(b *testing.B) {
	provider := cache.New(memory.New(), newFakeTransport(), cache.Config{
		Prefix: "benchmark", TTL: time.Minute,
	})
	key := settings.NewKey("benchmark", "value", settings.StringCodec{})
	change := settings.Change{Actor: "benchmark", Reason: "invalidation"}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = settings.Set(context.Background(), provider, settings.Global(), key, "value", change)
	}
}

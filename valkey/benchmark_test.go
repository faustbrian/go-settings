package valkey_test

import (
	"context"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	cache "github.com/faustbrian/golib/pkg/settings/valkey"
)

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

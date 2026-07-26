package settings_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestConcurrentRegistriesReadsWritesAndSnapshots(t *testing.T) {
	provider := memory.New()
	registry := settings.NewRegistry()
	key := settings.NewKey("race", "value", settings.IntCodec{})
	change := settings.Change{Actor: "race", Reason: "test"}
	chain := settings.Chain(settings.Tenant("tenant"), settings.Global())
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				_ = registry.RegisterNamespace(settings.NewNamespace(
					fmt.Sprintf("namespace-%d-%d", worker, iteration), "race"))
				_ = registry.Register(key)
				_, _ = registry.Lookup(key.StableID())
				_, _ = settings.Set(context.Background(), provider, settings.Global(), key,
					int64(worker*100+iteration), change)
				_, _ = settings.Resolve(context.Background(), provider, key, chain)
				snapshot, err := settings.Capture(context.Background(), provider, chain, key)
				if err == nil {
					_, _ = settings.ResolveSnapshot(snapshot, key, chain)
				}
			}
		}(worker)
	}
	wait.Wait()
}

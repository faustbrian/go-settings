package settings_test

import (
	"context"
	"reflect"
	"testing"
	"testing/quick"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestPropertyResolutionAlwaysChoosesFirstStoredOwner(t *testing.T) {
	t.Parallel()

	property := func(values [4]int16, present [4]bool) bool {
		provider := memory.New()
		key := settings.NewKey("property", "value", settings.IntCodec{})
		scopes := []settings.Scope{
			settings.User("user"), settings.Resource("resource"),
			settings.Tenant("tenant"), settings.Global(),
		}
		change := settings.Change{Actor: "property", Reason: "test"}
		wantStatus := settings.StatusMissing
		var want int64
		var wantOwner settings.Scope
		for index, scope := range scopes {
			if present[index] {
				if _, err := settings.Set(context.Background(), provider, scope, key, int64(values[index]), change); err != nil {
					return false
				}
				if wantStatus == settings.StatusMissing {
					want = int64(values[index])
					wantOwner = scope
					wantStatus = settings.StatusStored
					if index > 0 {
						wantStatus = settings.StatusInherited
					}
				}
			}
		}
		result, err := settings.Resolve(context.Background(), provider, key, settings.Chain(scopes...))
		return err == nil && result.Status == wantStatus && result.Value == want && result.Owner == wantOwner
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 500}); err != nil {
		t.Fatal(err)
	}
}

func TestPropertyUnrelatedKeysAndScopesCannotChangeResolution(t *testing.T) {
	t.Parallel()

	property := func(extraScope string, extraValue int64) bool {
		if extraScope == "" || len(extraScope) > 100 {
			return true
		}
		provider := memory.New()
		key := settings.NewKey("property", "target", settings.IntCodec{})
		other := settings.NewKey("property", "other", settings.IntCodec{})
		change := settings.Change{Actor: "property", Reason: "test"}
		_, _ = settings.Set(context.Background(), provider, settings.Global(), key, 7, change)
		before, err := settings.Resolve(context.Background(), provider, key, settings.Chain(settings.Tenant("tenant"), settings.Global()))
		if err != nil {
			return false
		}
		if scope := settings.Resource(extraScope); scope.Validate() == nil {
			_, _ = settings.Set(context.Background(), provider, scope, other, extraValue, change)
		}
		after, err := settings.Resolve(context.Background(), provider, key, settings.Chain(settings.Tenant("tenant"), settings.Global()))
		return err == nil && reflect.DeepEqual(before, after)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
}

func TestPropertySnapshotsRemainStableAfterArbitraryWrites(t *testing.T) {
	t.Parallel()

	property := func(before, after int64) bool {
		ctx := context.Background()
		provider := memory.New()
		key := settings.NewKey("property", "snapshot", settings.IntCodec{})
		chain := settings.Chain(settings.Global())
		change := settings.Change{Actor: "property", Reason: "test"}
		if _, err := settings.Set(ctx, provider, settings.Global(), key, before, change); err != nil {
			return false
		}
		snapshot, err := settings.Capture(ctx, provider, chain, key)
		if err != nil {
			return false
		}
		if _, err := settings.Set(ctx, provider, settings.Global(), key, after, change); err != nil {
			return false
		}
		frozen, err := settings.ResolveSnapshot(snapshot, key, chain)
		if err != nil {
			return false
		}
		live, err := settings.Resolve(ctx, provider, key, chain)
		return err == nil && frozen.Value == before && live.Value == after &&
			snapshot.Version() != ""
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
}

package settings_test

import (
	"context"
	"fmt"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func Example() {
	ctx := context.Background()
	provider := memory.New()
	theme := settings.NewKey("ui", "theme", settings.StringCodec{}, settings.WithDefault("system"))
	_, _ = settings.Set(ctx, provider, settings.Tenant("acme"), theme, "dark",
		settings.Change{Actor: "user:42", Reason: "preference update"})
	result, _ := settings.Resolve(ctx, provider, theme,
		settings.Chain(settings.User("7"), settings.Tenant("acme"), settings.Global()))
	fmt.Println(result.Value, result.Status == settings.StatusInherited)
	// Output: dark true
}

func ExampleCapture() {
	ctx := context.Background()
	provider := memory.New()
	key := settings.NewKey("jobs", "batch_size", settings.IntCodec{}, settings.WithDefault[int64](100))
	chain := settings.Chain(settings.Tenant("acme"), settings.Global())
	snapshot, _ := settings.Capture(ctx, provider, chain, key)
	result, _ := settings.ResolveSnapshot(snapshot, key, chain)
	fmt.Println(result.Value, snapshot.Version() != "")
	// Output: 100 true
}

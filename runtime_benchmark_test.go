package settings_test

import (
	"fmt"
	"testing"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func BenchmarkRuntimeHotRead(b *testing.B) {
	key := settings.NewKey("benchmark", "runtime-hot-read", settings.StringCodec{})
	durable := memory.New()
	if _, err := settings.Set(b.Context(), durable, settings.Global(), key, "value", settings.Change{
		Actor: "benchmark", Reason: "seed",
	}); err != nil {
		b.Fatal(err)
	}
	runtime := mustRuntime(b, durable, systemFleetClock{}, key)
	if err := runtime.Refresh(b.Context()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result, err := settings.ResolveCurrent(runtime, key)
		if err != nil || result.Value != "value" {
			b.Fatal(err)
		}
	}
}

func BenchmarkRuntimeRefresh100(b *testing.B) {
	durable := memory.New()
	definitions := make([]settings.Definition, 100)
	for index := range definitions {
		key := settings.NewKey("benchmark", fmt.Sprintf("runtime-key-%03d", index), settings.StringCodec{})
		definitions[index] = key
		if _, err := settings.Set(b.Context(), durable, settings.Global(), key, "value", settings.Change{
			Actor: "benchmark", Reason: "seed",
		}); err != nil {
			b.Fatal(err)
		}
	}
	runtime := mustRuntime(b, durable, systemFleetClock{}, definitions...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := runtime.Refresh(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

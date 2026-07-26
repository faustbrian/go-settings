package settings_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

type mode string

func TestBuiltInCodecsRoundTripTypedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		round func() (any, any, error)
	}{
		{"bool", func() (any, any, error) { return roundTrip(settings.BoolCodec{}, true) }},
		{"int", func() (any, any, error) { return roundTrip(settings.IntCodec{}, int64(-42)) }},
		{"decimal", func() (any, any, error) { return roundTrip(settings.DecimalCodec{}, settings.Decimal("12.340")) }},
		{"string", func() (any, any, error) { return roundTrip(settings.StringCodec{}, "hello") }},
		{"duration", func() (any, any, error) { return roundTrip(settings.DurationCodec{}, 3*time.Minute) }},
		{"time", func() (any, any, error) {
			return roundTrip(settings.TimeCodec{}, time.Date(2026, 7, 19, 9, 0, 0, 123, time.UTC))
		}},
		{"enum", func() (any, any, error) {
			return roundTrip(settings.NewEnumCodec("mode", mode("on"), mode("off")), mode("on"))
		}},
		{"strings", func() (any, any, error) { return roundTrip(settings.StringListCodec{}, []string{"a", "b"}) }},
		{"structured", func() (any, any, error) {
			return roundTrip(settings.JSONCodec[struct {
				Name string `json:"name"`
			}]{}, struct {
				Name string `json:"name"`
			}{Name: "A"})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			want, got, err := test.round()
			if err != nil {
				t.Fatalf("round trip: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("decoded = %#v, want %#v", got, want)
			}
		})
	}
}

func roundTrip[T any](codec settings.Codec[T], value T) (any, any, error) {
	encoded, err := codec.Encode(value)
	if err != nil {
		return value, nil, err
	}
	decoded, err := codec.Decode(encoded)
	return value, decoded, err
}

func TestSnapshotRemainsStableAcrossConcurrentWrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	key := settings.NewKey("ui", "theme", settings.StringCodec{})
	tenant := settings.Tenant("acme")
	chain := settings.Chain(tenant, settings.Global())
	change := settings.Change{Actor: "operator", Reason: "test"}
	first, err := settings.Set(ctx, provider, tenant, key, "light", change)
	if err != nil {
		t.Fatalf("set first: %v", err)
	}
	snapshot, err := settings.Capture(ctx, provider, chain, key)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if _, err := settings.Set(ctx, provider, tenant, key, "dark", change); err != nil {
		t.Fatalf("set second: %v", err)
	}

	result, err := settings.ResolveSnapshot(snapshot, key, chain)
	if err != nil {
		t.Fatalf("resolve snapshot: %v", err)
	}
	if result.Value != "light" || result.Version != first.Version {
		t.Fatalf("snapshot result = %#v", result)
	}
	if snapshot.Version() == "" {
		t.Fatal("snapshot version is empty")
	}
}

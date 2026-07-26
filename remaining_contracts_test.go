package settings_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

type getErrorProvider struct {
	settings.Provider
	err error
}

func (provider getErrorProvider) Get(context.Context, settings.Scope, string) (settings.Record, bool, error) {
	return settings.Record{}, false, provider.err
}

func TestRemainingCodecAndDefinitionBranches(t *testing.T) {
	t.Parallel()

	if value, err := (settings.BoolCodec{}).Decode([]byte("false")); err != nil || value {
		t.Fatalf("false decode = %v, %v", value, err)
	}
	oversizedListJSON := "[\"x\"" + strings.Repeat(",\"x\"", 10_000) + "]"
	if _, err := (settings.StringListCodec{}).Decode([]byte(oversizedListJSON)); err == nil {
		t.Fatal("decoded list accepted excessive items")
	}
	nilCodec := settings.NewKey[string]("valid", "nil", nil)
	if nilCodec.CodecID() != "" || nilCodec.CodecVersion() != 0 {
		t.Fatal("nil codec metadata was nonzero")
	}
	if err := nilCodec.ValidateEncoded(nil); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("invalid definition encoded validation = %v", err)
	}
	validated := settings.NewKey("valid", "count", settings.IntCodec{},
		settings.WithValidation(func(int64) error { return errors.New("invalid") }),
		settings.WithDefault[int64](0))
	if _, _, err := validated.DefaultEncoded(); err == nil {
		t.Fatal("invalid validated default encoded")
	}
	badEncodedDefault := settings.NewKey("valid", "encoded",
		failingStringCodec{id: "string", version: 1, encode: true}, settings.WithDefault("value"))
	if err := settings.NewRegistry().Register(badEncodedDefault); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("unencodable default = %v", err)
	}
	validating := settings.NewKey("valid", "accepted", settings.StringCodec{},
		settings.WithValidation(func(string) error { return nil }))
	if _, err := settings.PrepareSet(settings.Global(), validating, "value", nil,
		settings.Change{Actor: "test", Reason: "validation"}); err != nil {
		t.Fatalf("valid validator rejected value: %v", err)
	}
}

func TestRemainingOperationResolutionAndSnapshotBranches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	key := settings.NewKey("remaining", "value", settings.StringCodec{})
	change := settings.Change{Actor: "test", Reason: "remaining"}
	if _, err := settings.PrepareSet(settings.Global(), settings.NewKey[string]("remaining", "nil", nil), "value", nil, change); err == nil {
		t.Fatal("prepared invalid definition")
	}
	validatedKey := settings.NewKey("remaining", "validated", settings.StringCodec{},
		settings.WithValidation(func(string) error { return errors.New("invalid") }))
	if _, err := settings.PrepareSet(settings.Global(), validatedKey, "value", nil, change); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("prepared invalid value = %v", err)
	}
	if _, err := settings.CompareAndSet(ctx, provider, settings.Global(), key, "value", nil, settings.Change{}); err == nil {
		t.Fatal("compare-and-set accepted missing metadata")
	}
	if _, err := settings.Clear(ctx, provider, settings.Tenant(""), key, change); err == nil {
		t.Fatal("clear accepted invalid scope")
	}
	if _, err := settings.CompareAndClear(ctx, provider, settings.Global(), settings.NewKey[string]("remaining", "nil", nil), 0, change); err == nil {
		t.Fatal("compare-and-clear accepted invalid key")
	}
	if _, err := settings.Inherit(ctx, provider, settings.Global(), key, settings.Change{}); err == nil {
		t.Fatal("inherit accepted missing metadata")
	}
	if _, err := settings.CompareAndInherit(ctx, provider, settings.Global(), key, 0, settings.Change{}); err == nil {
		t.Fatal("compare-and-inherit accepted missing metadata")
	}
	if _, err := settings.Bulk(ctx, provider, []settings.Mutation{{}}, settings.AllowNonAtomic); err == nil {
		t.Fatal("bulk accepted invalid mutation")
	}

	if _, err := settings.Resolve(ctx, getErrorProvider{Provider: provider, err: errors.New("read")},
		key, settings.Chain(settings.Global())); err == nil {
		t.Fatal("resolution provider error hidden")
	}
	if _, err := settings.Resolve(ctx, provider, key, settings.Chain(settings.Tenant(""))); !errors.Is(err, settings.ErrInvalidScope) {
		t.Fatalf("resolution invalid scope = %v", err)
	}
	malformed := memory.New()
	_, _ = malformed.Apply(ctx, settings.Mutation{
		Scope: settings.Global(), Key: key.StableID(), Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "other", CodecVersion: 1, Change: change,
	})
	if _, err := settings.Resolve(ctx, malformed, key, settings.Chain(settings.Global())); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("codec mismatch = %v", err)
	}
	validatedProvider := memory.New()
	_, _ = validatedProvider.Apply(ctx, settings.Mutation{
		Scope: settings.Global(), Key: validatedKey.StableID(), Action: settings.ActionSet,
		Data: []byte("value"), CodecID: validatedKey.CodecID(), CodecVersion: validatedKey.CodecVersion(), Change: change,
	})
	if _, err := settings.Resolve(ctx, validatedProvider, validatedKey, settings.Chain(settings.Global())); err == nil {
		t.Fatal("resolved invalid persisted value")
	}

	snapshot, err := settings.Capture(ctx, provider, settings.Chain(settings.Global()), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Capture(ctx, provider, settings.Chain(), key); !errors.Is(err, settings.ErrInvalidChain) {
		t.Fatalf("capture invalid chain = %v", err)
	}
	if _, err := settings.Capture(ctx, providerOverride{Provider: provider, bulkGetErr: errors.New("read")},
		settings.Chain(settings.Global()), key); err == nil {
		t.Fatal("capture provider error hidden")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := snapshot.BulkGet(canceled, []settings.Scope{settings.Global()}, []string{key.StableID()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot canceled bulk = %v", err)
	}
}

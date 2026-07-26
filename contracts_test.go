package settings_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

type failingStringCodec struct {
	id      string
	version uint32
	encode  bool
	decode  bool
}

func (codec failingStringCodec) ID() string      { return codec.id }
func (codec failingStringCodec) Version() uint32 { return codec.version }
func (codec failingStringCodec) Encode(value string) ([]byte, error) {
	if codec.encode {
		return nil, errors.New("encode")
	}
	return []byte(value), nil
}
func (codec failingStringCodec) Decode(data []byte) (string, error) {
	if codec.decode {
		return "", errors.New("decode")
	}
	return string(data), nil
}

type failingCipher struct{ seal, open bool }

func (failingCipher) ID() string { return "failing" }
func (cipher failingCipher) Seal(data []byte) ([]byte, error) {
	if cipher.seal {
		return nil, errors.New("seal")
	}
	return data, nil
}
func (cipher failingCipher) Open(data []byte) ([]byte, error) {
	if cipher.open {
		return nil, errors.New("open")
	}
	return data, nil
}

func TestCodecFailureContracts(t *testing.T) {
	t.Parallel()

	if _, err := (settings.BoolCodec{}).Decode([]byte("TRUE")); err == nil {
		t.Fatal("bool accepted non-canonical data")
	}
	if _, err := (settings.DecimalCodec{}).Encode("01"); err == nil {
		t.Fatal("decimal accepted leading zero")
	}
	if _, err := (settings.DurationCodec{}).Decode([]byte("never")); err == nil {
		t.Fatal("duration accepted malformed data")
	}
	if _, err := (settings.TimeCodec{}).Decode([]byte("today")); err == nil {
		t.Fatal("time accepted malformed data")
	}
	enum := settings.NewEnumCodec("mode", "on", "off")
	if enum.ID() != "enum:mode" || enum.Version() != 1 {
		t.Fatalf("enum contract = %s@%d", enum.ID(), enum.Version())
	}
	if _, err := enum.Encode("other"); err == nil {
		t.Fatal("enum accepted unknown value")
	}
	list := make([]string, 10_001)
	if _, err := (settings.StringListCodec{}).Encode(list); err == nil {
		t.Fatal("list accepted excessive items")
	}
	if _, err := (settings.StringListCodec{}).Decode([]byte("{}")); err == nil {
		t.Fatal("list accepted object")
	}
	if _, err := (settings.JSONCodec[map[string]string]{}).Decode([]byte("{} {}")); err == nil {
		t.Fatal("JSON accepted trailing data")
	}
	if _, err := (settings.JSONCodec[string]{}).Decode(make([]byte, 1<<20+1)); err == nil {
		t.Fatal("JSON accepted oversized data")
	}

	encodeFailure := settings.NewEncryptionCodec(
		failingStringCodec{id: "string", version: 1, encode: true}, failingCipher{}, 1)
	if _, err := encodeFailure.Encode("secret"); err == nil {
		t.Fatal("encrypted codec hid inner encode failure")
	}
	sealFailure := settings.NewEncryptionCodec(settings.StringCodec{}, failingCipher{seal: true}, 1)
	if _, err := sealFailure.Encode("secret"); err == nil {
		t.Fatal("encrypted codec hid seal failure")
	}
	openFailure := settings.NewEncryptionCodec(settings.StringCodec{}, failingCipher{open: true}, 1)
	if _, err := openFailure.Decode([]byte("secret")); err == nil {
		t.Fatal("encrypted codec hid open failure")
	}
	decodeFailure := settings.NewEncryptionCodec(
		failingStringCodec{id: "string", version: 1, decode: true}, failingCipher{}, 1)
	if _, err := decodeFailure.Decode([]byte("secret")); err == nil {
		t.Fatal("encrypted codec hid inner decode failure")
	}
	invalid := settings.NewEncryptionCodec[string](nil, nil, 0)
	if invalid.ID() != "" {
		t.Fatalf("invalid encrypted codec ID = %q", invalid.ID())
	}
	if _, err := invalid.Encode("secret"); err == nil {
		t.Fatal("invalid encrypted codec encoded")
	}
	if _, err := invalid.Decode(nil); err == nil {
		t.Fatal("invalid encrypted codec decoded")
	}

	for name, contract := range map[string]struct {
		id      string
		version uint32
	}{
		"decimal":     {(settings.DecimalCodec{}).ID(), (settings.DecimalCodec{}).Version()},
		"duration":    {(settings.DurationCodec{}).ID(), (settings.DurationCodec{}).Version()},
		"time":        {(settings.TimeCodec{}).ID(), (settings.TimeCodec{}).Version()},
		"string-list": {(settings.StringListCodec{}).ID(), (settings.StringListCodec{}).Version()},
		"json":        {(settings.JSONCodec[string]{}).ID(), (settings.JSONCodec[string]{}).Version()},
	} {
		if contract.id != name || contract.version != 1 {
			t.Fatalf("codec contract = %s@%d", contract.id, contract.version)
		}
	}
}

func TestDefinitionRegistryAndScopeFailureContracts(t *testing.T) {
	t.Parallel()

	registry := settings.NewRegistry()
	if err := registry.RegisterNamespace(settings.NewNamespace("bad/name", "")); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("invalid namespace = %v", err)
	}
	if err := registry.Register(nil); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("nil definition = %v", err)
	}
	invalidKeys := []settings.Definition{
		settings.NewKey("", "name", settings.StringCodec{}),
		settings.NewKey("bad/name", "name", settings.StringCodec{}),
		settings.NewKey[string]("valid", "name", nil),
		settings.NewKey("valid", "name", failingStringCodec{}),
		settings.NewKey("valid", "name", failingStringCodec{id: "string"}),
	}
	for _, key := range invalidKeys {
		if err := registry.Register(key); !errors.Is(err, settings.ErrInvalidDefinition) {
			t.Fatalf("invalid definition %q = %v", key.StableID(), err)
		}
	}
	validated := settings.NewKey("valid", "count", settings.IntCodec{},
		settings.WithValidation(func(value int64) error {
			if value < 1 {
				return errors.New("positive required")
			}
			return nil
		}), settings.WithDefault[int64](0))
	if err := registry.Register(validated); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("invalid default = %v", err)
	}
	key := settings.NewKey("valid", "name", settings.StringCodec{}, settings.WithDefault("default"))
	if err := registry.Register(key); err != nil {
		t.Fatal(err)
	}
	incompatible := settings.NewKey("valid", "name", settings.IntCodec{})
	if err := registry.Register(incompatible); !errors.Is(err, settings.ErrIncompatibleDefinition) {
		t.Fatalf("incompatible definition = %v", err)
	}
	data, ok, err := key.DefaultEncoded()
	if err != nil || !ok || string(data) != "default" || key.String() != key.StableID() {
		t.Fatalf("default = %q, %v, %v", data, ok, err)
	}
	withoutDefault := settings.NewKey("valid", "other", settings.StringCodec{})
	if data, ok, err := withoutDefault.DefaultEncoded(); err != nil || ok || data != nil {
		t.Fatalf("missing default = %q, %v, %v", data, ok, err)
	}
	if err := key.ValidateEncoded([]byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := (settings.NewKey("valid", "bad", failingStringCodec{id: "string", version: 1, decode: true})).
		ValidateEncoded([]byte("value")); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("decode validation = %v", err)
	}

	invalidScopes := []settings.Scope{
		{Kind: "unknown"}, {Kind: settings.ScopeGlobal, ID: "id"},
		settings.Tenant(""), settings.User(strings.Repeat("x", 256)), settings.Resource("bad\nvalue"),
	}
	for _, scope := range invalidScopes {
		if !errors.Is(scope.Validate(), settings.ErrInvalidScope) {
			t.Fatalf("scope accepted: %#v", scope)
		}
	}
	if settings.Resource("r").String() != "resource:r" {
		t.Fatal("resource string mismatch")
	}
}

func TestOperationsResolutionAndSnapshotFailureContracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	key := settings.NewKey("contracts", "value", settings.StringCodec{})
	change := settings.Change{Actor: "test", Reason: "contract"}
	if _, err := settings.PrepareSet(settings.Tenant(""), key, "value", nil, change); err == nil {
		t.Fatal("prepared invalid scope")
	}
	badKey := settings.NewKey("contracts", "bad", failingStringCodec{id: "string", version: 1, encode: true})
	if _, err := settings.PrepareSet(settings.Global(), badKey, "value", nil, change); err == nil {
		t.Fatal("prepared failed encoding")
	}
	if _, err := settings.Set(ctx, provider, settings.Global(), key, "value", settings.Change{}); !errors.Is(err, settings.ErrInvalidChange) {
		t.Fatalf("missing audit metadata = %v", err)
	}
	record, err := settings.Set(ctx, provider, settings.Global(), key, "value", change)
	if err != nil {
		t.Fatal(err)
	}
	expected := record.Version
	if _, err := settings.CompareAndSet(ctx, provider, settings.Global(), key, "next", &expected, change); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.CompareAndInherit(ctx, provider, settings.Global(), key, expected, change); !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("stale inherit = %v", err)
	}
	if _, err := settings.Bulk(ctx, noAtomicProvider{Provider: provider}, []settings.Mutation{{}}, settings.RequireAtomic); !errors.Is(err, settings.ErrUnsupported) {
		t.Fatalf("required atomicity = %v", err)
	}

	if _, err := settings.Resolve(ctx, provider, key, settings.Chain()); !errors.Is(err, settings.ErrInvalidChain) {
		t.Fatalf("empty chain = %v", err)
	}
	if _, err := settings.Resolve(ctx, provider, key, settings.Chain(settings.Global(), settings.Global())); !errors.Is(err, settings.ErrInvalidChain) {
		t.Fatalf("duplicate chain = %v", err)
	}
	snapshot, err := settings.Capture(ctx, provider, settings.Chain(settings.Global()), key)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Capabilities().Snapshots {
		t.Fatal("snapshot capability absent")
	}
	if _, err := snapshot.BulkGet(ctx, []settings.Scope{settings.Global()}, []string{key.StableID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Apply(ctx, settings.Mutation{}); !errors.Is(err, settings.ErrUnsupported) {
		t.Fatalf("snapshot apply = %v", err)
	}
	if _, err := snapshot.BulkApply(ctx, nil); !errors.Is(err, settings.ErrUnsupported) {
		t.Fatalf("snapshot bulk = %v", err)
	}
	if _, err := snapshot.History(ctx, settings.HistoryQuery{}); !errors.Is(err, settings.ErrUnsupported) {
		t.Fatalf("snapshot history = %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := snapshot.Get(canceled, settings.Global(), key.StableID()); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot canceled get = %v", err)
	}
	if _, err := settings.Capture(ctx, provider, settings.Chain(settings.Global()), nil); err == nil {
		t.Fatal("snapshot accepted nil definition")
	}
	if _, err := settings.Capture(ctx, provider, settings.Chain(settings.Global()), key, key); !errors.Is(err, settings.ErrDuplicateDefinition) {
		t.Fatalf("snapshot duplicate = %v", err)
	}
}

type noAtomicProvider struct{ settings.Provider }

func (provider noAtomicProvider) Capabilities() settings.Capabilities {
	capabilities := provider.Provider.Capabilities()
	capabilities.AtomicBulk = false
	return capabilities
}

func TestMutationBoundaryContracts(t *testing.T) {
	t.Parallel()

	valid := settings.Mutation{
		Scope: settings.Global(), Key: "namespace/key", Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1,
		Change: settings.Change{Actor: "actor", Reason: "reason", At: time.Now()},
	}
	mutations := []settings.Mutation{
		{Scope: settings.Tenant(""), Key: valid.Key, Action: valid.Action, CodecID: valid.CodecID, CodecVersion: 1, Change: valid.Change},
		{Scope: valid.Scope, Action: valid.Action, CodecID: valid.CodecID, CodecVersion: 1, Change: valid.Change},
		{Scope: valid.Scope, Key: valid.Key, Action: 99, CodecID: valid.CodecID, CodecVersion: 1, Change: valid.Change},
		{Scope: valid.Scope, Key: valid.Key, Action: valid.Action, CodecVersion: 1, Change: valid.Change},
		{Scope: valid.Scope, Key: valid.Key, Action: settings.ActionClear, Data: []byte("bad"), CodecID: valid.CodecID, CodecVersion: 1, Change: valid.Change},
		{Scope: valid.Scope, Key: valid.Key, Action: valid.Action, Data: make([]byte, 1<<20+1), CodecID: valid.CodecID, CodecVersion: 1, Change: valid.Change},
		{Scope: valid.Scope, Key: valid.Key, Action: valid.Action, CodecID: valid.CodecID, CodecVersion: 1},
	}
	for index, mutation := range mutations {
		if settings.ValidateMutation(mutation) == nil {
			t.Fatalf("mutation %d accepted", index)
		}
	}
	if err := settings.ValidateMutation(valid); err != nil {
		t.Fatal(err)
	}
}

package settings_test

import (
	"context"
	"errors"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

type providerOverride struct {
	settings.Provider
	records      []settings.Record
	bulkGetErr   error
	bulkApplyErr error
	atomic       *bool
}

func (provider providerOverride) Capabilities() settings.Capabilities {
	capabilities := provider.Provider.Capabilities()
	if provider.atomic != nil {
		capabilities.AtomicBulk = *provider.atomic
	}
	return capabilities
}
func (provider providerOverride) BulkGet(context.Context, []settings.Scope, []string) ([]settings.Record, error) {
	if provider.bulkGetErr != nil || provider.records != nil {
		return provider.records, provider.bulkGetErr
	}
	return nil, nil
}
func (provider providerOverride) BulkApply(ctx context.Context, mutations []settings.Mutation) ([]settings.Record, error) {
	if provider.bulkApplyErr != nil {
		return nil, provider.bulkApplyErr
	}
	return provider.Provider.BulkApply(ctx, mutations)
}

func TestExportFailureAndSensitiveOptInContracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	key := settings.NewKey("export", "value", settings.StringCodec{})
	options := settings.ExportOptions{Schema: "app/v1"}
	if _, err := settings.Export(ctx, provider, []settings.Scope{settings.Global()}, []settings.Definition{key}, settings.ExportOptions{}); err == nil {
		t.Fatal("empty export schema accepted")
	}
	if _, err := settings.Export(ctx, provider, nil, []settings.Definition{key}, options); err == nil {
		t.Fatal("empty export scopes accepted")
	}
	if _, err := settings.Export(ctx, provider, []settings.Scope{settings.Global()}, []settings.Definition{nil}, options); err == nil {
		t.Fatal("nil export definition accepted")
	}
	if _, err := settings.Export(ctx, provider, []settings.Scope{settings.Global()}, []settings.Definition{key, key}, options); !errors.Is(err, settings.ErrDuplicateDefinition) {
		t.Fatalf("duplicate export definition = %v", err)
	}
	badDefault := settings.NewKey("export", "bad", failingStringCodec{id: "string", version: 1, encode: true},
		settings.WithDefault("value"))
	if _, err := settings.Export(ctx, provider, []settings.Scope{settings.Global()}, []settings.Definition{badDefault}, options); err == nil {
		t.Fatal("bad encoded default accepted")
	}
	if _, err := settings.Export(ctx, providerOverride{Provider: provider, bulkGetErr: errors.New("read")},
		[]settings.Scope{settings.Global()}, []settings.Definition{key}, options); err == nil {
		t.Fatal("provider export error hidden")
	}
	if _, err := settings.Export(ctx, providerOverride{Provider: provider, records: []settings.Record{{Key: "unknown"}}},
		[]settings.Scope{settings.Global()}, []settings.Definition{key}, options); err == nil {
		t.Fatal("unknown provider key accepted")
	}

	sensitive := settings.NewKey("export", "secret", settings.StringCodec{},
		settings.WithSensitive[string](), settings.WithDefault("default-secret"))
	change := settings.Change{Actor: "operator", Reason: "test"}
	_, _ = settings.Set(ctx, provider, settings.Global(), sensitive, "stored-secret", change)
	document, err := settings.Export(ctx, provider, []settings.Scope{settings.Global()},
		[]settings.Definition{sensitive}, settings.ExportOptions{Schema: "app/v1", IncludeSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	redactedDocument, err := settings.Export(ctx, provider, []settings.Scope{settings.Global()},
		[]settings.Definition{sensitive}, settings.ExportOptions{Schema: "app/v1"})
	if err != nil || !redactedDocument.Definitions[0].Redacted {
		t.Fatalf("redacted default export = %#v, %v", redactedDocument, err)
	}
	otherScope := settings.Tenant("other")
	_, _ = settings.Set(ctx, provider, otherScope, sensitive, "other-secret", change)
	if _, err := settings.Export(ctx, provider, []settings.Scope{otherScope, settings.Global()},
		[]settings.Definition{sensitive}, settings.ExportOptions{Schema: "app/v1"}); err != nil {
		t.Fatalf("multi-scope export: %v", err)
	}
	if string(document.Definitions[0].Default) != "default-secret" ||
		string(document.Entries[0].Data) != "stored-secret" || document.ExportedAt.IsZero() {
		t.Fatalf("opted-in export = %#v", document)
	}
}

func TestImportValidationAndStateContracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	registry := settings.NewRegistry()
	key := settings.NewKey("import", "value", settings.StringCodec{})
	if err := registry.Register(key); err != nil {
		t.Fatal(err)
	}
	change := settings.Change{Actor: "operator", Reason: "import"}
	valid := settings.ExportDocument{
		Format: settings.ExportFormat, Version: 1, Schema: "app/v1",
		Entries: []settings.ExportEntry{{
			Scope: settings.Global(), Key: key.StableID(), State: settings.StateValue,
			Data: []byte("value"), CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
		}},
	}
	cases := []settings.ExportDocument{
		{},
		{Format: settings.ExportFormat, Version: 1, Schema: "app/v1"},
	}
	for index, document := range cases {
		if _, err := settings.Import(ctx, provider, registry, document, settings.ImportOptions{Change: change}); err == nil {
			t.Fatalf("invalid import %d accepted", index)
		}
	}
	if _, err := settings.Import(ctx, provider, registry, valid,
		settings.ImportOptions{ExpectedSchema: "app/v2", Change: change}); err == nil {
		t.Fatal("schema mismatch accepted")
	}
	nonAtomic := false
	if _, err := settings.Import(ctx, providerOverride{Provider: provider, atomic: &nonAtomic}, registry, valid,
		settings.ImportOptions{Change: change}); !errors.Is(err, settings.ErrUnsupported) {
		t.Fatalf("non-atomic import = %v", err)
	}
	duplicate := valid
	duplicate.Entries = append(append([]settings.ExportEntry(nil), valid.Entries...), valid.Entries[0])
	if _, err := settings.Import(ctx, provider, registry, duplicate, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("duplicate import coordinate accepted")
	}
	redacted := valid
	redacted.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	redacted.Entries[0].Redacted = true
	if _, err := settings.Import(ctx, provider, registry, redacted, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("redacted import accepted")
	}
	unknown := valid
	unknown.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	unknown.Entries[0].Key = "unknown/key"
	if _, err := settings.Import(ctx, provider, registry, unknown, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("unknown import key accepted")
	}
	incompatible := valid
	incompatible.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	incompatible.Entries[0].CodecVersion = 2
	if _, err := settings.Import(ctx, provider, registry, incompatible, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("incompatible import accepted")
	}
	invalidState := valid
	invalidState.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	invalidState.Entries[0].State = 99
	if _, err := settings.Import(ctx, provider, registry, invalidState, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("invalid import state accepted")
	}

	cleared := valid
	cleared.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	cleared.Entries[0].State = settings.StateCleared
	cleared.Entries[0].Data = nil
	if _, err := settings.Import(ctx, provider, registry, cleared, settings.ImportOptions{Change: change}); err != nil {
		t.Fatalf("clear import: %v", err)
	}
	inherited := valid
	inherited.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	inherited.Entries[0].State = settings.StateMissing
	inherited.Entries[0].Data = nil
	if _, err := settings.Import(ctx, provider, registry, inherited, settings.ImportOptions{Change: change}); err != nil {
		t.Fatalf("inherit import: %v", err)
	}
	if _, err := settings.Import(ctx, providerOverride{Provider: provider, bulkApplyErr: errors.New("write")}, registry,
		valid, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("provider import error hidden")
	}
	integerRegistry := settings.NewRegistry()
	integerKey := settings.NewKey("import", "integer", settings.IntCodec{})
	if err := integerRegistry.Register(integerKey); err != nil {
		t.Fatal(err)
	}
	invalidValue := valid
	invalidValue.Entries = []settings.ExportEntry{{
		Scope: settings.Global(), Key: integerKey.StableID(), State: settings.StateValue,
		Data: []byte("invalid"), CodecID: integerKey.CodecID(), CodecVersion: integerKey.CodecVersion(),
	}}
	if _, err := settings.Import(ctx, provider, integerRegistry, invalidValue, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("invalid encoded import accepted")
	}
	invalidScope := valid
	invalidScope.Entries = append([]settings.ExportEntry(nil), valid.Entries...)
	invalidScope.Entries[0].Scope = settings.Tenant("")
	if _, err := settings.Import(ctx, provider, registry, invalidScope, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("invalid import scope accepted")
	}
}

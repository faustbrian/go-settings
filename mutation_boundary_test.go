package settings_test

import (
	"strings"
	"testing"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestEncryptionCodecRejectsEachIncompleteContractIndependently(t *testing.T) {
	t.Parallel()

	validCipher := failingCipher{}
	tests := []struct {
		name  string
		codec settings.Codec[string]
	}{
		{name: "missing inner", codec: settings.NewEncryptionCodec[string](nil, validCipher, 1)},
		{name: "missing cipher", codec: settings.NewEncryptionCodec[string](settings.StringCodec{}, nil, 1)},
		{name: "zero version", codec: settings.NewEncryptionCodec(settings.StringCodec{}, validCipher, 0)},
		{name: "empty cipher id", codec: settings.NewEncryptionCodec(settings.StringCodec{}, blankIDCipher{}, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.codec.ID() != "" && (test.name == "missing inner" || test.name == "missing cipher") {
				t.Fatalf("incomplete codec ID = %q", test.codec.ID())
			}
			if _, err := test.codec.Encode("secret"); err == nil {
				t.Fatal("incomplete codec encoded")
			}
			if _, err := test.codec.Decode([]byte("secret")); err == nil {
				t.Fatal("incomplete codec decoded")
			}
		})
	}
}

func TestCollectionCodecsEnforceExactResourceBoundaries(t *testing.T) {
	t.Parallel()

	list := make([]string, 10_000)
	if _, err := (settings.StringListCodec{}).Encode(list); err != nil {
		t.Fatalf("exact list bound rejected: %v", err)
	}
	if _, err := (settings.StringListCodec{}).Encode(append(list, "overflow")); err == nil {
		t.Fatal("list above exact bound accepted")
	}
	jsonAtLimit := make([]byte, 1<<20)
	jsonAtLimit[0] = '"'
	for index := 1; index < len(jsonAtLimit)-1; index++ {
		jsonAtLimit[index] = 'a'
	}
	jsonAtLimit[len(jsonAtLimit)-1] = '"'
	decoded, err := (settings.JSONCodec[string]{}).Decode(jsonAtLimit)
	if err != nil || len(decoded) != len(jsonAtLimit)-2 {
		t.Fatalf("JSON at exact bound = (%d bytes, %v)", len(decoded), err)
	}
	if _, err := (settings.JSONCodec[string]{}).Decode(append(jsonAtLimit, ' ')); err == nil {
		t.Fatal("JSON above exact bound accepted")
	}
}

func TestExportCoordinateBoundsRedactionAndOrdering(t *testing.T) {
	t.Parallel()

	durable := memory.New()
	definitions := make([]settings.Definition, 1_000)
	for index := range definitions {
		definitions[index] = settings.NewKey("boundary", strings.Repeat("0", 3-lenInt(index))+intString(index), settings.StringCodec{})
	}
	options := settings.ExportOptions{Schema: "app/v1"}
	if _, err := settings.Export(t.Context(), durable, []settings.Scope{settings.Global()}, definitions, options); err != nil {
		t.Fatalf("exact export coordinate bound rejected: %v", err)
	}
	if _, err := settings.Export(t.Context(), durable,
		[]settings.Scope{settings.Global(), settings.Tenant("a")}, definitions[:501], options); err == nil {
		t.Fatal("export above coordinate bound accepted")
	}

	plain := settings.NewKey("export-order", "plain", settings.StringCodec{}, settings.WithDefault("plain-default"))
	secretWithoutDefault := settings.NewKey("export-order", "secret", settings.StringCodec{}, settings.WithSensitive[string]())
	document, err := settings.Export(t.Context(), durable, []settings.Scope{settings.Global()},
		[]settings.Definition{plain, secretWithoutDefault}, options)
	if err != nil {
		t.Fatal(err)
	}
	if document.Definitions[0].Redacted || string(document.Definitions[0].Default) != "plain-default" ||
		document.Definitions[1].HasDefault || document.Definitions[1].Redacted {
		t.Fatalf("default redaction contract = %+v", document.Definitions)
	}

	orderedProvider := providerOverride{Provider: durable, records: []settings.Record{
		{Scope: settings.Tenant("z"), Key: plain.StableID()},
		{Scope: settings.Global(), Key: secretWithoutDefault.StableID()},
		{Scope: settings.Global(), Key: plain.StableID()},
	}}
	ordered, err := settings.Export(t.Context(), orderedProvider,
		[]settings.Scope{settings.Tenant("z"), settings.Global()},
		[]settings.Definition{secretWithoutDefault, plain}, options)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{
		ordered.Entries[0].Scope.String() + "/" + ordered.Entries[0].Key,
		ordered.Entries[1].Scope.String() + "/" + ordered.Entries[1].Key,
		ordered.Entries[2].Scope.String() + "/" + ordered.Entries[2].Key,
	}
	want := []string{
		"global/export-order/plain", "global/export-order/secret", "tenant:z/export-order/plain",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ordered entries = %v", got)
		}
	}
}

func TestImportEnvelopeAndEntryCountBoundaries(t *testing.T) {
	t.Parallel()

	durable := memory.New()
	provider := providerOverride{Provider: durable, acceptBulk: true}
	registry := settings.NewRegistry()
	entries := make([]settings.ExportEntry, 1_000)
	for index := range entries {
		name := strings.Repeat("0", 3-lenInt(index)) + intString(index)
		key := settings.NewKey("import-boundary", name, settings.StringCodec{})
		if err := registry.Register(key); err != nil {
			t.Fatal(err)
		}
		entries[index] = settings.ExportEntry{
			Scope: settings.Global(), Key: key.StableID(), State: settings.StateValue,
			Data: []byte("value"), CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
		}
	}
	valid := settings.ExportDocument{Format: settings.ExportFormat, Version: 1, Schema: "app/v1", Entries: entries}
	options := settings.ImportOptions{Change: settings.Change{Actor: "operator", Reason: "boundary import"}}
	if records, err := settings.Import(t.Context(), provider, registry, valid, options); err != nil || len(records) != 1_000 {
		t.Fatalf("exact import bound = (%d records, %v)", len(records), err)
	}
	overflow := valid
	overflow.Entries = append(append([]settings.ExportEntry(nil), entries...), entries[0])
	if _, err := settings.Import(t.Context(), provider, registry, overflow, options); err == nil {
		t.Fatal("import above exact bound accepted")
	}
	empty := valid
	empty.Entries = nil
	if _, err := settings.Import(t.Context(), provider, registry, empty, options); err == nil {
		t.Fatal("empty import accepted")
	}
	for _, corrupt := range []func(*settings.ExportDocument){
		func(document *settings.ExportDocument) { document.Format = "other" },
		func(document *settings.ExportDocument) { document.Version = 2 },
		func(document *settings.ExportDocument) { document.Schema = "" },
	} {
		document := valid
		document.Entries = document.Entries[:1]
		corrupt(&document)
		if _, err := settings.Import(t.Context(), provider, registry, document, options); err == nil {
			t.Fatal("independently malformed import envelope accepted")
		}
	}
}

func TestDefinitionMutationAndNamespaceExactBoundaries(t *testing.T) {
	t.Parallel()
	if err := settings.Tenant(strings.Repeat("t", 255)).Validate(); err != nil {
		t.Fatalf("exact scope identifier bound rejected: %v", err)
	}
	if err := settings.Tenant(strings.Repeat("t", 256)).Validate(); err == nil {
		t.Fatal("overlong scope identifier accepted")
	}

	if err := settings.NewKey(strings.Repeat("n", 128), strings.Repeat("k", 383), settings.StringCodec{}).ValidateDefinition(); err != nil {
		t.Fatalf("exact definition bounds rejected: %v", err)
	}
	for _, key := range []settings.Definition{
		settings.NewKey(strings.Repeat("n", 129), "key", settings.StringCodec{}),
		settings.NewKey("namespace", strings.Repeat("k", 384), settings.StringCodec{}),
		settings.NewKey("namespace", "", settings.StringCodec{}),
	} {
		if key.ValidateDefinition() == nil {
			t.Fatalf("invalid key boundary accepted: %q", key.StableID())
		}
	}

	valid := settings.Mutation{
		Scope: settings.Global(), Key: strings.Repeat("k", 512), Action: settings.ActionSet,
		Data: make([]byte, 1<<20), CodecID: strings.Repeat("c", 255), CodecVersion: 1,
		Change: settings.Change{Actor: strings.Repeat("a", 255), Reason: strings.Repeat("r", 2_000)},
	}
	if err := settings.ValidateMutation(valid); err != nil {
		t.Fatalf("exact mutation bounds rejected: %v", err)
	}
	invalid := []settings.Mutation{
		func() settings.Mutation { value := valid; value.Key += "x"; return value }(),
		func() settings.Mutation { value := valid; value.CodecID += "x"; return value }(),
		func() settings.Mutation { value := valid; value.Data = append(value.Data, 0); return value }(),
		func() settings.Mutation { value := valid; value.Change.Actor += "x"; return value }(),
		func() settings.Mutation { value := valid; value.Change.Reason += "x"; return value }(),
		func() settings.Mutation { value := valid; value.Change.Actor = ""; return value }(),
		func() settings.Mutation { value := valid; value.Change.Reason = ""; return value }(),
	}
	for index, mutation := range invalid {
		if settings.ValidateMutation(mutation) == nil {
			t.Fatalf("invalid mutation boundary %d accepted", index)
		}
	}

	registry := settings.NewRegistry()
	if err := registry.RegisterNamespace(settings.NewNamespace(strings.Repeat("n", 128), "")); err != nil {
		t.Fatalf("exact namespace bound rejected: %v", err)
	}
	for _, id := range []string{"", strings.Repeat("n", 129), "bad/name", "bad\nname"} {
		if err := registry.RegisterNamespace(settings.NewNamespace(id, "")); err == nil {
			t.Fatalf("invalid namespace %q accepted", id)
		}
	}
}

type blankIDCipher struct{}

func (blankIDCipher) ID() string                       { return "" }
func (blankIDCipher) Seal(data []byte) ([]byte, error) { return data, nil }
func (blankIDCipher) Open(data []byte) ([]byte, error) { return data, nil }

func lenInt(value int) int { return len(intString(value)) }

func intString(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var reversed [20]byte
	index := len(reversed)
	for value > 0 {
		index--
		reversed[index] = digits[value%10]
		value /= 10
	}
	return string(reversed[index:])
}

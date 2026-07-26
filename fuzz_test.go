package settings_test

import (
	"context"
	"encoding/json"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func FuzzCodecsRejectMalformedPersistedDataWithoutPanicking(f *testing.F) {
	for _, seed := range [][]byte{[]byte("true"), []byte("FALSE"), []byte("[\"a\"]"), []byte("{"), nil} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = (settings.BoolCodec{}).Decode(data)
		_, _ = (settings.DecimalCodec{}).Decode(data)
		_, _ = (settings.StringListCodec{}).Decode(data)
		_, _ = (settings.JSONCodec[map[string]any]{}).Decode(data)
	})
}

func FuzzScopeIdentifiers(f *testing.F) {
	for _, seed := range []string{"tenant", "", "a\x00b", "line\nbreak"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		scope := settings.Tenant(id)
		if scope.Validate() == nil && scope.String() != "tenant:"+id {
			t.Fatalf("valid scope string = %q", scope.String())
		}
	})
}

func FuzzImportDocumentsFailClosed(f *testing.F) {
	f.Add([]byte(`{"format":"go-settings/export","version":1}`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2<<20 {
			t.Skip()
		}
		var document settings.ExportDocument
		if json.Unmarshal(data, &document) != nil {
			return
		}
		registry := settings.NewRegistry()
		key := settings.NewKey("fuzz", "value", settings.StringCodec{})
		if err := registry.Register(key); err != nil {
			t.Fatal(err)
		}
		_, _ = settings.Import(context.Background(), memory.New(), registry, document,
			settings.ImportOptions{Change: settings.Change{Actor: "fuzz", Reason: "test"}})
	})
}

func FuzzResolutionOfMalformedStoredValues(f *testing.F) {
	f.Add([]byte("42"))
	f.Add([]byte("not-an-int"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		provider := memory.New()
		key := settings.NewKey("fuzz", "integer", settings.IntCodec{})
		_, err := provider.Apply(context.Background(), settings.Mutation{
			Scope: settings.Global(), Key: key.StableID(), Action: settings.ActionSet,
			Data: data, CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
			Change: settings.Change{Actor: "fuzz", Reason: "test"},
		})
		if err != nil {
			return
		}
		_, _ = settings.Resolve(context.Background(), provider, key, settings.Chain(settings.Global()))
	})
}

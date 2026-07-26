package settings_test

import (
	"context"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestExportImportRoundTripWithSchemaMetadataAndRedaction(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	source := memory.New()
	target := memory.New()
	registry := settings.NewRegistry()
	name := settings.NewKey("profile", "name", settings.StringCodec{})
	token := settings.NewKey("profile", "token", settings.StringCodec{}, settings.WithSensitive[string]())
	for _, definition := range []settings.Definition{name, token} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	scope := settings.Tenant("acme")
	change := settings.Change{Actor: "operator", Reason: "test"}
	if _, err := settings.Set(ctx, source, scope, name, "Acme", change); err != nil {
		t.Fatalf("set name: %v", err)
	}
	if _, err := settings.Set(ctx, source, scope, token, "secret", change); err != nil {
		t.Fatalf("set token: %v", err)
	}

	document, err := settings.Export(ctx, source, []settings.Scope{scope},
		[]settings.Definition{name, token}, settings.ExportOptions{
			Schema: "application/v3", At: time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC),
		})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if document.Format != settings.ExportFormat || document.Version != 1 ||
		document.Schema != "application/v3" || !document.Entries[1].Redacted {
		t.Fatalf("document metadata or redaction = %#v", document)
	}
	if _, err := settings.Import(ctx, target, registry, document, settings.ImportOptions{Change: change}); err == nil {
		t.Fatal("import accepted a redacted value")
	}

	document.Entries = document.Entries[:1]
	result, err := settings.Import(ctx, target, registry, document, settings.ImportOptions{Change: change})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("imported %d records, want 1", len(result))
	}
	resolved, err := settings.Resolve(ctx, target, name, settings.Chain(scope))
	if err != nil || resolved.Value != "Acme" {
		t.Fatalf("resolved imported value = %#v, %v", resolved, err)
	}
}

package settings_test

import (
	"errors"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
)

func TestRegistryRejectsDuplicateStableKey(t *testing.T) {
	t.Parallel()

	registry := settings.NewRegistry()
	key := settings.NewKey("billing", "invoice.due_days", settings.IntCodec{},
		settings.WithDocumentation[int64]("Days before an invoice is due."),
	)

	if err := registry.Register(key); err != nil {
		t.Fatalf("register first definition: %v", err)
	}
	if err := registry.Register(key); !errors.Is(err, settings.ErrDuplicateDefinition) {
		t.Fatalf("register duplicate error = %v, want ErrDuplicateDefinition", err)
	}
}

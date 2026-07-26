package migration_test

import (
	"context"
	"strings"
	"testing"
	"testing/quick"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	"github.com/faustbrian/golib/pkg/settings/migration"
)

type versionedStringCodec struct{ version uint32 }

func (codec versionedStringCodec) ID() string                    { return "versioned-string" }
func (codec versionedStringCodec) Version() uint32               { return codec.version }
func (versionedStringCodec) Encode(value string) ([]byte, error) { return []byte(value), nil }
func (versionedStringCodec) Decode(data []byte) (string, error)  { return string(data), nil }

func TestRunnerIsResumableAndStepsRemainIdempotentAfterLostCheckpoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	provider := memory.New()
	scope := settings.Tenant("acme")
	oldKey := settings.NewKey("profile", "display_name", versionedStringCodec{version: 1})
	renamedKey := settings.NewKey("profile", "name", versionedStringCodec{version: 1})
	upgradedKey := settings.NewKey("profile", "name", versionedStringCodec{version: 2})
	change := settings.Change{Actor: "migration", Reason: "schema v2"}
	if _, err := settings.Set(ctx, provider, scope, oldKey, "acme", change); err != nil {
		t.Fatalf("seed: %v", err)
	}
	plan := migration.Plan{
		ID: "profile-v2", FromSchema: "v1", ToSchema: "v2",
		Steps: []migration.Step{
			migration.Rename("rename-display-name", oldKey, renamedKey),
			migration.Transform("uppercase-name", renamedKey, upgradedKey,
				func(data []byte) ([]byte, error) { return []byte(strings.ToUpper(string(data))), nil }),
			migration.ChangeDefault("new-default", upgradedKey, []byte("anonymous"), []byte("guest")),
		},
	}
	if _, err := migration.Run(ctx, provider, migration.NewMemoryJournal(), plan, []settings.Scope{scope}, change); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first, ok, err := provider.Get(ctx, scope, upgradedKey.StableID())
	if err != nil || !ok || string(first.Data) != "ACME" || first.CodecVersion != 2 {
		t.Fatalf("first migrated record = %#v, %v, %v", first, ok, err)
	}

	// A lost journal simulates a crash after durable writes but before the
	// checkpoint commit. Every step must safely recognize its completed state.
	if _, err := migration.Run(ctx, provider, migration.NewMemoryJournal(), plan, []settings.Scope{scope}, change); err != nil {
		t.Fatalf("second run: %v", err)
	}
	second, ok, err := provider.Get(ctx, scope, upgradedKey.StableID())
	if err != nil || !ok || second.Version != first.Version {
		t.Fatalf("second migrated record = %#v, %v, %v", second, ok, err)
	}
}

func TestPropertyRenamePreservesArbitraryValuesAndIsIdempotent(t *testing.T) {
	t.Parallel()

	property := func(value int64) bool {
		ctx := context.Background()
		provider := memory.New()
		oldKey := settings.NewKey("property", "old", settings.IntCodec{})
		newKey := settings.NewKey("property", "new", settings.IntCodec{})
		change := settings.Change{Actor: "property", Reason: "migration"}
		if _, err := settings.Set(ctx, provider, settings.Global(), oldKey, value, change); err != nil {
			return false
		}
		plan := migration.Plan{
			ID: "property-rename", FromSchema: "v1", ToSchema: "v2",
			Steps: []migration.Step{migration.Rename("rename", oldKey, newKey)},
		}
		if _, err := migration.Run(ctx, provider, migration.NewMemoryJournal(), plan,
			[]settings.Scope{settings.Global()}, change); err != nil {
			return false
		}
		first, err := settings.Resolve(ctx, provider, newKey,
			settings.Chain(settings.Global()))
		if err != nil || first.Value != value {
			return false
		}
		if _, err := migration.Run(ctx, provider, migration.NewMemoryJournal(), plan,
			[]settings.Scope{settings.Global()}, change); err != nil {
			return false
		}
		second, err := settings.Resolve(ctx, provider, newKey,
			settings.Chain(settings.Global()))
		return err == nil && second.Value == value && second.Version == first.Version
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 300}); err != nil {
		t.Fatal(err)
	}
}

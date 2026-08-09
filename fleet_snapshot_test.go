package settings_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestFleetSnapshotIsActivatedOnlyAfterCompleteValidation(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	key := settings.NewKey("fleet", "mode", settings.StringCodec{},
		settings.WithValidation(func(value string) error {
			if value != "safe" {
				return errors.New("unsafe mode")
			}
			return nil
		}),
	)
	durable := memory.NewWithClock(func() time.Time { return now })
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "initialize fleet test",
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := settings.CaptureWithOptions(t.Context(), durable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now,
			Provenance: settings.ProvenancePostgreSQL,
		}, key)
	if err != nil {
		t.Fatal(err)
	}
	metadata := snapshot.Metadata(now.Add(3 * time.Second))
	if metadata.Revision == "" || metadata.Revision != snapshot.Version() ||
		metadata.Provenance != settings.ProvenancePostgreSQL || metadata.CapturedAt != now ||
		metadata.Age != 3*time.Second {
		t.Fatalf("metadata = %+v", metadata)
	}

	malformed := corruptingProvider{Provider: durable, corrupt: func(record settings.Record) settings.Record {
		record.Data = []byte("unsafe")
		return record
	}}
	invalid, err := settings.CaptureWithOptions(t.Context(), malformed,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now.Add(time.Second),
			Provenance: settings.ProvenancePostgreSQL,
		}, key)
	if !errors.Is(err, settings.ErrInvalidValue) || invalid.Version() != "" {
		t.Fatalf("malformed capture = (%q, %v), want zero snapshot and invalid value", invalid.Version(), err)
	}

	resolved, err := settings.ResolveSnapshot(snapshot, key, settings.Chain(settings.Global()))
	if err != nil || resolved.Value != "safe" {
		t.Fatalf("last known good = (%+v, %v)", resolved, err)
	}
}

func TestSettingClassesMakeFailurePolicyExplicit(t *testing.T) {
	t.Parallel()

	standard := settings.NewKey("fleet", "theme", settings.StringCodec{})
	secret := settings.NewKey("fleet", "credential", settings.StringCodec{}, settings.WithSensitive[string]())
	security := settings.NewKey("fleet", "authorization-mode", settings.StringCodec{},
		settings.WithClass[string](settings.ClassSecuritySensitive),
	)
	explicitSecret := settings.NewKey("fleet", "explicit-secret", settings.StringCodec{},
		settings.WithClass[string](settings.ClassSecret),
	)
	explicitStandard := settings.NewKey("fleet", "explicit-standard", settings.StringCodec{},
		settings.WithClass[string](settings.ClassStandard),
	)

	if settings.ClassOf(standard) != settings.ClassStandard || standard.Sensitive() {
		t.Fatalf("standard class = %v, sensitive = %v", settings.ClassOf(standard), standard.Sensitive())
	}
	if settings.ClassOf(legacyDefinition{definition: standard}) != settings.ClassStandard {
		t.Fatal("mixed-version definition did not default to the standard class")
	}
	if settings.ClassOf(secret) != settings.ClassSecret || !secret.Sensitive() {
		t.Fatalf("secret class = %v, sensitive = %v", settings.ClassOf(secret), secret.Sensitive())
	}
	if settings.ClassOf(security) != settings.ClassSecuritySensitive || !security.Sensitive() {
		t.Fatalf("security class = %v, sensitive = %v", settings.ClassOf(security), security.Sensitive())
	}
	if !explicitSecret.Sensitive() || explicitStandard.Sensitive() {
		t.Fatalf("explicit sensitivity = (secret %v, standard %v)", explicitSecret.Sensitive(), explicitStandard.Sensitive())
	}

	invalid := settings.NewKey("fleet", "invalid", settings.StringCodec{},
		settings.WithClass[string](settings.SettingClass(255)),
	)
	if !errors.Is(invalid.ValidateDefinition(), settings.ErrInvalidDefinition) {
		t.Fatalf("invalid class accepted: %v", invalid.ValidateDefinition())
	}
}

func TestCaptureRejectsEveryMalformedRecordBoundary(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	key := settings.NewKey("fleet", "mode", settings.StringCodec{})
	durable := memory.NewWithClock(func() time.Time { return now })
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "safe", settings.Change{
		Actor: "operator", Reason: "snapshot validation",
	}); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name    string
		corrupt func(settings.Record) settings.Record
	}{
		{"unknown scope", func(record settings.Record) settings.Record { record.Scope = settings.Tenant("other"); return record }},
		{"unknown key", func(record settings.Record) settings.Record { record.Key = "fleet/other"; return record }},
		{"zero version", func(record settings.Record) settings.Record { record.Version = 0; return record }},
		{"zero update time", func(record settings.Record) settings.Record { record.UpdatedAt = time.Time{}; return record }},
		{"codec id", func(record settings.Record) settings.Record { record.CodecID = "other"; return record }},
		{"codec version", func(record settings.Record) settings.Record { record.CodecVersion++; return record }},
		{"oversized value", func(record settings.Record) settings.Record { record.Data = make([]byte, 1<<20+1); return record }},
		{"cleared data", func(record settings.Record) settings.Record {
			record.State = settings.StateCleared
			record.Data = []byte("data")
			return record
		}},
		{"missing data", func(record settings.Record) settings.Record {
			record.State = settings.StateMissing
			record.Data = []byte("data")
			return record
		}},
		{"unknown state", func(record settings.Record) settings.Record { record.State = settings.State(255); return record }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			provider := corruptingProvider{Provider: durable, corrupt: test.corrupt}
			if _, err := settings.CaptureWithOptions(t.Context(), provider,
				settings.Chain(settings.Global()), settings.CaptureOptions{
					CapturedAt: now, Provenance: settings.ProvenancePostgreSQL,
				}, key); !errors.Is(err, settings.ErrInvalidValue) {
				t.Fatalf("malformed record error = %v", err)
			}
		})
	}

	duplicate := duplicateRecordProvider{Provider: durable}
	if _, err := settings.CaptureWithOptions(t.Context(), duplicate,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now, Provenance: settings.ProvenancePostgreSQL,
		}, key); !errors.Is(err, settings.ErrInvalidValue) {
		t.Fatalf("duplicate coordinate error = %v", err)
	}
	if _, err := settings.CaptureWithOptions(t.Context(), durable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: time.Time{}, Provenance: settings.ProvenancePostgreSQL,
		}, key); err == nil {
		t.Fatal("zero capture time accepted")
	}
	if _, err := settings.CaptureWithOptions(t.Context(), durable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now, Provenance: "unknown",
		}, key); err == nil {
		t.Fatal("unknown provenance accepted")
	}
	if _, err := settings.CaptureWithOptions(t.Context(), durable,
		settings.Chain(settings.Global()), settings.CaptureOptions{
			CapturedAt: now, Provenance: settings.ProvenancePostgreSQL,
		}, settings.NewKey("", "invalid", settings.StringCodec{})); !errors.Is(err, settings.ErrInvalidDefinition) {
		t.Fatalf("invalid definition error = %v", err)
	}
}

type corruptingProvider struct {
	settings.Provider
	corrupt func(settings.Record) settings.Record
}

type duplicateRecordProvider struct{ settings.Provider }

func (provider duplicateRecordProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	records, err := provider.Provider.BulkGet(ctx, scopes, keys)
	return append(records, records...), err
}

type legacyDefinition struct{ definition settings.Definition }

func (definition legacyDefinition) StableID() string     { return definition.definition.StableID() }
func (definition legacyDefinition) Namespace() string    { return definition.definition.Namespace() }
func (definition legacyDefinition) Name() string         { return definition.definition.Name() }
func (definition legacyDefinition) CodecID() string      { return definition.definition.CodecID() }
func (definition legacyDefinition) CodecVersion() uint32 { return definition.definition.CodecVersion() }
func (definition legacyDefinition) Documentation() string {
	return definition.definition.Documentation()
}
func (definition legacyDefinition) DisplayName() string { return definition.definition.DisplayName() }
func (definition legacyDefinition) Sensitive() bool     { return definition.definition.Sensitive() }
func (definition legacyDefinition) DefaultEncoded() ([]byte, bool, error) {
	return definition.definition.DefaultEncoded()
}
func (definition legacyDefinition) ValidateEncoded(data []byte) error {
	return definition.definition.ValidateEncoded(data)
}
func (definition legacyDefinition) ValidateDefinition() error {
	return definition.definition.ValidateDefinition()
}

func (provider corruptingProvider) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	records, err := provider.Provider.BulkGet(ctx, scopes, keys)
	if err != nil {
		return nil, err
	}
	for index := range records {
		records[index] = provider.corrupt(records[index])
	}
	return records, nil
}

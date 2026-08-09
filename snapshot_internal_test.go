package settings

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSnapshotWireEnforcesExactResourceAndDocumentBoundaries(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	key := NewKey("internal", "wire-boundary", StringCodec{})
	chain := Chain(Global())
	snapshot, err := CaptureWithOptions(t.Context(), internalSnapshotProvider{}, chain,
		CaptureOptions{CapturedAt: now, Provenance: ProvenanceProvider}, key)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreSnapshot(nil, chain, key); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("empty snapshot restore error = %v", err)
	}
	exact := append(append([]byte(nil), encoded...), make([]byte, 2<<20-len(encoded))...)
	for index := len(encoded); index < len(exact); index++ {
		exact[index] = ' '
	}
	if _, err := RestoreSnapshot(exact, chain, key); err != nil {
		t.Fatalf("exactly 2 MiB snapshot restore error = %v", err)
	}
	if _, err := RestoreSnapshot(append(exact, ' '), chain, key); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("over-2 MiB snapshot restore error = %v", err)
	}
	if _, err := RestoreSnapshot(append(encoded, []byte("{}")...), chain, key); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("trailing JSON document restore error = %v", err)
	}

	makeSnapshot := func(keyLength int) Snapshot {
		record := Record{
			Scope: Global(), Key: strings.Repeat("k", keyLength), State: StateValue,
			Data: []byte("v"), CodecID: "string", CodecVersion: 1,
			Version: 1, UpdatedAt: now,
		}
		return Snapshot{
			records: map[snapshotCoordinate]Record{{scope: record.Scope, key: record.Key}: record},
			version: "revision",
			metadata: SnapshotMetadata{
				Revision: "revision", Provenance: ProvenanceProvider,
				Origin: ProvenanceProvider, CapturedAt: now,
			},
		}
	}
	base, err := makeSnapshot(1).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	exactMarshal := makeSnapshot(1 + (2<<20 - len(base)))
	exactData, err := exactMarshal.MarshalBinary()
	if err != nil || len(exactData) != 2<<20 {
		t.Fatalf("exactly 2 MiB snapshot marshal = (%d bytes, %v)", len(exactData), err)
	}
	if _, err := makeSnapshot(2 + (2<<20 - len(base))).MarshalBinary(); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("over-2 MiB snapshot marshal error = %v", err)
	}
}

func TestSnapshotMarshalRejectsEachIncompleteMetadataField(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	valid := Snapshot{
		records: map[snapshotCoordinate]Record{}, version: "revision",
		metadata: SnapshotMetadata{
			Revision: "revision", Provenance: ProvenanceProvider,
			Origin: ProvenanceProvider, CapturedAt: now,
		},
	}
	for name, snapshot := range map[string]Snapshot{
		"revision": func() Snapshot {
			candidate := valid
			candidate.version = ""
			return candidate
		}(),
		"capture time": func() Snapshot {
			candidate := valid
			candidate.metadata.CapturedAt = time.Time{}
			return candidate
		}(),
		"origin": func() Snapshot {
			candidate := valid
			candidate.metadata.Origin = Provenance("invalid")
			return candidate
		}(),
	} {
		if _, err := snapshot.MarshalBinary(); !errors.Is(err, ErrInvalidValue) {
			t.Errorf("%s metadata error = %v", name, err)
		}
	}
}

func TestSnapshotCaptureAcceptsTheExactValueLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	key := NewKey("internal", "value-boundary", StringCodec{})
	record := Record{
		Scope: Global(), Key: key.StableID(), State: StateValue,
		Data: []byte(strings.Repeat("x", 1<<20)), CodecID: key.CodecID(), CodecVersion: key.CodecVersion(),
		Version: 1, UpdatedAt: now,
	}
	provider := fixedSnapshotProvider{internalSnapshotProvider: internalSnapshotProvider{}, records: []Record{record}}
	if _, err := CaptureWithOptions(t.Context(), provider, Chain(Global()),
		CaptureOptions{CapturedAt: now, Provenance: ProvenanceProvider}, key); err != nil {
		t.Fatalf("exactly 1 MiB record capture error = %v", err)
	}
	provider.records[0].Data = append(provider.records[0].Data, 'x')
	if _, err := CaptureWithOptions(t.Context(), provider, Chain(Global()),
		CaptureOptions{CapturedAt: now, Provenance: ProvenanceProvider}, key); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("over-1 MiB record capture error = %v", err)
	}
}

func TestSnapshotCapturePreservesTheValidationFailureCause(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0).UTC()
	key := NewKey("internal", "validation-cause", StringCodec{})
	validationFailure := errors.New("definition rejected persisted value")
	definition := rejectingSnapshotDefinition{Definition: key, err: validationFailure}
	provider := fixedSnapshotProvider{records: []Record{{
		Scope: Global(), Key: key.StableID(), State: StateValue, Data: []byte("value"),
		CodecID: key.CodecID(), CodecVersion: key.CodecVersion(), Version: 1, UpdatedAt: now,
	}}}
	_, err := CaptureWithOptions(t.Context(), provider, Chain(Global()),
		CaptureOptions{CapturedAt: now, Provenance: ProvenanceProvider}, definition)
	if !errors.Is(err, ErrInvalidValue) || !errors.Is(err, validationFailure) {
		t.Fatalf("snapshot validation error = %v", err)
	}
}

func TestSnapshotMarshalOrdersScopesThenKeys(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	records := []Record{
		{Scope: Tenant("z"), Key: "b", State: StateValue, Data: []byte("1"), CodecID: "string", CodecVersion: 1, Version: 1, UpdatedAt: now},
		{Scope: Global(), Key: "b", State: StateValue, Data: []byte("2"), CodecID: "string", CodecVersion: 1, Version: 1, UpdatedAt: now},
		{Scope: Tenant("a"), Key: "z", State: StateValue, Data: []byte("3"), CodecID: "string", CodecVersion: 1, Version: 1, UpdatedAt: now},
		{Scope: Tenant("a"), Key: "a", State: StateValue, Data: []byte("4"), CodecID: "string", CodecVersion: 1, Version: 1, UpdatedAt: now},
	}
	snapshot := Snapshot{
		records: make(map[snapshotCoordinate]Record, len(records)), version: "revision",
		metadata: SnapshotMetadata{
			Revision: "revision", Provenance: ProvenanceProvider,
			Origin: ProvenanceProvider, CapturedAt: now,
		},
	}
	for _, record := range records {
		snapshot.records[snapshotCoordinate{scope: record.Scope, key: record.Key}] = record
	}
	data, err := snapshot.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var wire snapshotWire
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(wire.Records))
	for _, record := range wire.Records {
		got = append(got, record.Scope.String()+"/"+record.Key)
	}
	want := []string{"global/b", "tenant:a/a", "tenant:a/z", "tenant:z/b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("snapshot record order = %v, want %v", got, want)
	}
}

type fixedSnapshotProvider struct {
	internalSnapshotProvider
	records []Record
}

type rejectingSnapshotDefinition struct {
	Definition
	err error
}

func (definition rejectingSnapshotDefinition) ValidateEncoded([]byte) error { return definition.err }

func (provider fixedSnapshotProvider) BulkGet(_ context.Context, _ []Scope, _ []string) ([]Record, error) {
	return append([]Record(nil), provider.records...), nil
}

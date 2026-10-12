package settings

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type snapshotCoordinate struct {
	scope Scope
	key   string
}

// Snapshot is an immutable point-in-time read view for request and job
// consistency of captured encoded records. It copies provider payloads and is
// safe for concurrent reads, but does not deep-copy arbitrary codec outputs or
// Key defaults. Zero Snapshot is an empty read view with no valid cache document.
// Coherence at capture is the backing provider's responsibility.
type Snapshot struct {
	records  map[snapshotCoordinate]Record
	version  string
	metadata SnapshotMetadata
}

// Provenance identifies the boundary that supplied a complete snapshot.
type Provenance string

const (
	// ProvenanceProvider labels a generic provider capture.
	ProvenanceProvider Provenance = "provider"
	// ProvenancePostgreSQL labels a caller-declared PostgreSQL capture.
	ProvenancePostgreSQL Provenance = "postgresql"
	// ProvenanceValkey labels a caller-declared Valkey boundary.
	ProvenanceValkey Provenance = "valkey"
	// ProvenanceSnapshotCache labels restored cached state, retaining its Origin.
	ProvenanceSnapshotCache Provenance = "snapshot-cache"
	// ProvenanceDefaults labels definition-only fallback.
	ProvenanceDefaults Provenance = "defaults"
)

// CaptureOptions records where and when a snapshot was obtained.
type CaptureOptions struct {
	// CapturedAt records capture time; capture requires nonzero and normalizes it to UTC.
	CapturedAt time.Time
	// Provenance describes the immediate source, not authenticated proof of origin.
	Provenance Provenance
}

// SnapshotMetadata describes immutable snapshot identity and freshness.
type SnapshotMetadata struct {
	// Revision is an unkeyed content identifier, not a signature or freshness guarantee.
	Revision string
	// Provenance describes the immediate source, not authenticated proof of origin.
	Provenance Provenance
	// Origin retains the source provenance across snapshot-cache restoration.
	Origin Provenance
	// CapturedAt records capture time; capture requires nonzero and normalizes it to UTC.
	CapturedAt time.Time
	// Age is nonnegative elapsed time at Metadata evaluation; clock rollback clamps it to zero.
	Age time.Duration
}

// Capture reads all requested definitions and owners in one provider bulk
// operation and freezes the returned records.
func Capture(ctx context.Context, provider Provider, chain ResolutionChain, definitions ...Definition) (Snapshot, error) {
	return CaptureWithOptions(ctx, provider, chain, CaptureOptions{
		CapturedAt: time.Now().UTC(),
		Provenance: ProvenanceProvider,
	}, definitions...)
}

// CaptureWithOptions captures and completely validates a snapshot before it
// can become observable. Metadata/definitions/coordinates and each supplied
// record are validated; omitted coordinates are treated as absent. The provider
// must return a coherent complete bulk read. There is no capture-wide allocation
// cap beyond record validation; callers must bound the coordinate set.
func CaptureWithOptions(ctx context.Context, provider Provider, chain ResolutionChain, options CaptureOptions, definitions ...Definition) (Snapshot, error) {
	keys, definitionsByID, err := prepareSnapshot(chain, options, definitions)
	if err != nil {
		return Snapshot{}, err
	}
	records, err := provider.BulkGet(ctx, chain.Scopes(), keys)
	if err != nil {
		return Snapshot{}, err
	}
	return assembleSnapshot(chain, options, keys, definitionsByID, records)
}

func prepareSnapshot(chain ResolutionChain, options CaptureOptions, definitions []Definition) ([]string, map[string]Definition, error) {
	if err := chain.validate(); err != nil {
		return nil, nil, err
	}
	if options.CapturedAt.IsZero() || !validProvenance(options.Provenance) {
		return nil, nil, fmt.Errorf("settings: invalid snapshot metadata")
	}
	keys := make([]string, 0, len(definitions))
	definitionsByID := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if definition == nil {
			return nil, nil, fmt.Errorf("settings: nil snapshot definition")
		}
		if err := definition.ValidateDefinition(); err != nil {
			return nil, nil, err
		}
		if _, ok := definitionsByID[definition.StableID()]; ok {
			return nil, nil, fmt.Errorf("%w: %s", ErrDuplicateDefinition, definition.StableID())
		}
		definitionsByID[definition.StableID()] = definition
		keys = append(keys, definition.StableID())
	}
	return keys, definitionsByID, nil
}

func assembleSnapshot(chain ResolutionChain, options CaptureOptions, keys []string, definitionsByID map[string]Definition, records []Record) (Snapshot, error) {
	frozen := make(map[snapshotCoordinate]Record, len(records))
	for _, record := range records {
		definition, ok := definitionsByID[record.Key]
		if !ok || !chainContains(chain, record.Scope) {
			return Snapshot{}, fmt.Errorf("%w: unexpected snapshot coordinate", ErrInvalidValue)
		}
		coordinate := snapshotCoordinate{scope: record.Scope, key: record.Key}
		if _, duplicate := frozen[coordinate]; duplicate {
			return Snapshot{}, fmt.Errorf("%w: duplicate snapshot coordinate", ErrInvalidValue)
		}
		if record.Version == 0 || record.UpdatedAt.IsZero() ||
			record.CodecID != definition.CodecID() || record.CodecVersion != definition.CodecVersion() {
			return Snapshot{}, fmt.Errorf("%w: record contract for %s", ErrInvalidValue, record.Key)
		}
		switch record.State {
		case StateValue:
			if len(record.Data) > 1<<20 {
				return Snapshot{}, fmt.Errorf("%w: record size for %s", ErrInvalidValue, record.Key)
			}
			if err := definition.ValidateEncoded(record.Data); err != nil {
				return Snapshot{}, fmt.Errorf("%w: encoded record for %s: %w", ErrInvalidValue, record.Key, err)
			}
		case StateMissing, StateCleared:
			if len(record.Data) != 0 {
				return Snapshot{}, fmt.Errorf("%w: non-value record data for %s", ErrInvalidValue, record.Key)
			}
		default:
			return Snapshot{}, fmt.Errorf("%w: record state for %s", ErrInvalidValue, record.Key)
		}
		record.Data = append([]byte(nil), record.Data...)
		frozen[coordinate] = record
	}
	version := snapshotVersion(chain, keys, records)
	return Snapshot{
		records: frozen,
		version: version,
		metadata: SnapshotMetadata{
			Revision: version, Provenance: options.Provenance, Origin: options.Provenance,
			CapturedAt: options.CapturedAt.UTC(),
		},
	}, nil
}

const snapshotWireVersion uint16 = 1

type snapshotWire struct {
	Schema     uint16     `json:"schema"`
	Revision   string     `json:"revision"`
	Origin     Provenance `json:"origin"`
	CapturedAt time.Time  `json:"captured_at"`
	Records    []Record   `json:"records"`
}

// MarshalBinary encodes a bounded, versioned snapshot cache document. The
// caller owns encryption and durable storage of sensitive values.
func (snapshot Snapshot) MarshalBinary() ([]byte, error) {
	if snapshot.version == "" {
		return nil, fmt.Errorf("%w: empty snapshot", ErrInvalidValue)
	}
	if snapshot.metadata.CapturedAt.IsZero() {
		return nil, fmt.Errorf("%w: empty snapshot", ErrInvalidValue)
	}
	if !validProvenance(snapshot.metadata.Origin) {
		return nil, fmt.Errorf("%w: empty snapshot", ErrInvalidValue)
	}
	records := make([]Record, 0, len(snapshot.records))
	for _, record := range snapshot.records {
		record.Data = append([]byte(nil), record.Data...)
		records = append(records, record)
	}
	slices.SortFunc(records, func(left, right Record) int {
		return cmp.Or(
			strings.Compare(left.Scope.String(), right.Scope.String()),
			strings.Compare(left.Key, right.Key),
		)
	})
	data, _ := json.Marshal(snapshotWire{
		Schema: snapshotWireVersion, Revision: snapshot.version, Origin: snapshot.metadata.Origin,
		CapturedAt: snapshot.metadata.CapturedAt, Records: records,
	})
	if len(data) > 2<<20 {
		return nil, fmt.Errorf("%w: snapshot exceeds 2 MiB", ErrInvalidValue)
	}
	return data, nil
}

// RestoreSnapshot completely decodes, validates, and revision-checks a cached
// snapshot before returning it as last-known-good state. It accepts one strict
// JSON document of at most 2 MiB and checks current definitions. Revision is an
// unkeyed checksum, not authentication: freshness timestamps and provenance are
// not covered by it. Callers must authenticate storage and assess age/policy
// before activation; a valid document alone is not proof of trusted origin.
func RestoreSnapshot(data []byte, chain ResolutionChain, definitions ...Definition) (Snapshot, error) {
	if len(data) == 0 {
		return Snapshot{}, fmt.Errorf("%w: snapshot cache size", ErrInvalidValue)
	}
	if len(data) > 2<<20 {
		return Snapshot{}, fmt.Errorf("%w: snapshot cache size", ErrInvalidValue)
	}
	var wire snapshotWire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Snapshot{}, fmt.Errorf("%w: decode snapshot cache", ErrInvalidValue)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Snapshot{}, fmt.Errorf("%w: trailing snapshot cache data", ErrInvalidValue)
	}
	if wire.Schema != snapshotWireVersion || wire.Revision == "" || wire.CapturedAt.IsZero() ||
		!validProvenance(wire.Origin) || wire.Origin == ProvenanceDefaults || wire.Origin == ProvenanceSnapshotCache {
		return Snapshot{}, fmt.Errorf("%w: snapshot cache contract", ErrInvalidValue)
	}
	options := CaptureOptions{CapturedAt: wire.CapturedAt, Provenance: ProvenanceSnapshotCache}
	keys, definitionsByID, err := prepareSnapshot(chain, options, definitions)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := assembleSnapshot(chain, options, keys, definitionsByID, wire.Records)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.version != wire.Revision {
		return Snapshot{}, fmt.Errorf("%w: snapshot revision mismatch", ErrInvalidValue)
	}
	snapshot.metadata.Origin = wire.Origin
	return snapshot, nil
}

func validProvenance(provenance Provenance) bool {
	switch provenance {
	case ProvenanceProvider, ProvenancePostgreSQL, ProvenanceValkey, ProvenanceSnapshotCache, ProvenanceDefaults:
		return true
	default:
		return false
	}
}

func chainContains(chain ResolutionChain, candidate Scope) bool {
	for _, scope := range chain.scopes {
		if scope == candidate {
			return true
		}
	}
	return false
}

// The digest identifies selected coordinates and encoded record content, not
// freshness or authority. Sorting makes input enumeration order irrelevant;
// capture timestamps/provenance and chain precedence are not authenticated.
func snapshotVersion(chain ResolutionChain, keys []string, records []Record) string {
	parts := make([]string, 0, len(records)+len(keys)+len(chain.scopes))
	for _, scope := range chain.scopes {
		parts = append(parts, "scope="+scope.String())
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, "key="+key)
	}
	for _, record := range records {
		parts = append(parts, record.Scope.String()+"/"+record.Key+"/"+
			strconv.FormatUint(record.Version, 10)+"/"+strconv.Itoa(int(record.State))+"/"+
			record.CodecID+"/"+strconv.FormatUint(uint64(record.CodecVersion), 10)+"/"+
			hex.EncodeToString(record.Data))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// Version returns a stable content identifier for the snapshot.
func (snapshot Snapshot) Version() string { return snapshot.version }

// Metadata returns snapshot identity plus age at now. Clock rollback never
// produces a negative age.
func (snapshot Snapshot) Metadata(now time.Time) SnapshotMetadata {
	metadata := snapshot.metadata
	metadata.Age = now.Sub(metadata.CapturedAt)
	metadata.Age = max(metadata.Age, 0)
	return metadata
}

// ResolveSnapshot resolves without consulting the mutable backing provider.
func ResolveSnapshot[T any](snapshot Snapshot, key Key[T], chain ResolutionChain) (Result[T], error) {
	return Resolve(context.Background(), snapshot, key, chain)
}

// Capabilities advertises immutable snapshot reads only.
func (Snapshot) Capabilities() Capabilities { return Capabilities{Snapshots: true} }

// Get returns a copied encoded payload; tombstones return false and zero Snapshot is empty.
func (snapshot Snapshot) Get(ctx context.Context, scope Scope, key string) (Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, false, err
	}
	record, ok := snapshot.records[snapshotCoordinate{scope: scope, key: key}]
	record.Data = append([]byte(nil), record.Data...)
	return record, ok && record.State != StateMissing, nil
}

// BulkGet returns copies in scopes/keys iteration order and omits missing tombstones; empty
// inputs return no records.
func (snapshot Snapshot) BulkGet(ctx context.Context, scopes []Scope, keys []string) ([]Record, error) {
	var records []Record
	for _, scope := range scopes {
		for _, key := range keys {
			record, ok, err := snapshot.Get(ctx, scope, key)
			if err != nil {
				return nil, err
			}
			if ok {
				records = append(records, record)
			}
		}
	}
	return records, nil
}

// Apply always returns ErrUnsupported; captured state cannot be mutated.
func (Snapshot) Apply(context.Context, Mutation) (Record, error) {
	return Record{}, ErrUnsupported
}

// BulkApply always returns ErrUnsupported.
func (Snapshot) BulkApply(context.Context, []Mutation) ([]Record, error) {
	return nil, ErrUnsupported
}

// History always returns ErrUnsupported; snapshots do not retain audit history.
func (Snapshot) History(context.Context, HistoryQuery) ([]ChangeRecord, error) {
	return nil, ErrUnsupported
}

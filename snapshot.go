package settings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type snapshotCoordinate struct {
	scope Scope
	key   string
}

// Snapshot is an immutable point-in-time read view for request and job
// consistency.
type Snapshot struct {
	records map[snapshotCoordinate]Record
	version string
}

// Capture reads all requested definitions and owners in one provider bulk
// operation and freezes the returned records.
func Capture(ctx context.Context, provider Provider, chain ResolutionChain, definitions ...Definition) (Snapshot, error) {
	if err := chain.validate(); err != nil {
		return Snapshot{}, err
	}
	keys := make([]string, 0, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition == nil {
			return Snapshot{}, fmt.Errorf("settings: nil snapshot definition")
		}
		if _, ok := seen[definition.StableID()]; ok {
			return Snapshot{}, fmt.Errorf("%w: %s", ErrDuplicateDefinition, definition.StableID())
		}
		seen[definition.StableID()] = struct{}{}
		keys = append(keys, definition.StableID())
	}
	records, err := provider.BulkGet(ctx, chain.Scopes(), keys)
	if err != nil {
		return Snapshot{}, err
	}
	frozen := make(map[snapshotCoordinate]Record, len(records))
	for _, record := range records {
		record.Data = append([]byte(nil), record.Data...)
		frozen[snapshotCoordinate{scope: record.Scope, key: record.Key}] = record
	}
	return Snapshot{records: frozen, version: snapshotVersion(chain, keys, records)}, nil
}

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
			strconv.FormatUint(record.Version, 10)+"/"+strconv.Itoa(int(record.State)))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// Version returns a stable content identifier for the snapshot.
func (snapshot Snapshot) Version() string { return snapshot.version }

// ResolveSnapshot resolves without consulting the mutable backing provider.
func ResolveSnapshot[T any](snapshot Snapshot, key Key[T], chain ResolutionChain) (Result[T], error) {
	return Resolve(context.Background(), snapshot, key, chain)
}

func (Snapshot) Capabilities() Capabilities { return Capabilities{Snapshots: true} }
func (snapshot Snapshot) Get(ctx context.Context, scope Scope, key string) (Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, false, err
	}
	record, ok := snapshot.records[snapshotCoordinate{scope: scope, key: key}]
	record.Data = append([]byte(nil), record.Data...)
	return record, ok, nil
}
func (snapshot Snapshot) BulkGet(ctx context.Context, scopes []Scope, keys []string) ([]Record, error) {
	records := make([]Record, 0, len(scopes)*len(keys))
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
func (Snapshot) Apply(context.Context, Mutation) (Record, error) {
	return Record{}, ErrUnsupported
}
func (Snapshot) BulkApply(context.Context, []Mutation) ([]Record, error) {
	return nil, ErrUnsupported
}
func (Snapshot) History(context.Context, HistoryQuery) ([]ChangeRecord, error) {
	return nil, ErrUnsupported
}

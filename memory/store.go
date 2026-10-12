// Package memory provides a concurrent, non-durable provider with atomic batches
// and coherent captures. Use New or NewWithClock. It owns no goroutines or close
// lifecycle; values, version tombstones, sticky sensitivity and unbounded history
// live for the Store lifetime. It does not authorize owners or encrypt values.
package memory

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	settings "github.com/faustbrian/go-settings/v3"
)

type coordinate struct {
	scope settings.Scope
	key   string
}

// Store is a deterministic concurrent provider for tests/local applications.
// Construct it with New; zero Store cannot write. Returned buffers are copied,
// including history, and caller inputs must not change during calls. The custom
// clock runs under the write lock and must not block, panic or reenter Store.
type Store struct {
	mu        sync.RWMutex
	records   map[coordinate]settings.Record
	versions  map[coordinate]uint64
	sensitive map[coordinate]bool
	history   []settings.ChangeRecord
	now       func() time.Time
}

// New constructs an empty store using the system clock.
func New() *Store { return NewWithClock(time.Now) }

// NewWithClock constructs a store with a borrowed clock. It must be nonnil and
// safe under synchronous locked writes; a nil clock panics on zero-time changes.
func NewWithClock(now func() time.Time) *Store {
	return &Store{
		records:   make(map[coordinate]settings.Record),
		versions:  make(map[coordinate]uint64),
		sensitive: make(map[coordinate]bool),
		now:       now,
	}
}

// Capabilities advertises CAS, atomic bulk, history and coherent snapshots; no subscriptions.
func (*Store) Capabilities() settings.Capabilities {
	return settings.Capabilities{CompareAndSet: true, AtomicBulk: true, History: true, Snapshots: true}
}

// Get returns a copied record payload; missing tombstones return false while retaining version
// metadata. Context is checked before lock acquisition.
func (store *Store) Get(ctx context.Context, scope settings.Scope, key string) (settings.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return settings.Record{}, false, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, ok := store.records[coordinate{scope: scope, key: key}]
	record.Data = append([]byte(nil), record.Data...)
	return record, ok && record.State != settings.StateMissing, nil
}

// BulkGet copies a coherent Cartesian-product view under one read lock, including missing
// tombstones. No coordinate-count limit is imposed here.
func (store *Store) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	var records []settings.Record
	for _, scope := range scopes {
		for _, key := range keys {
			if record, ok := store.records[coordinate{scope: scope, key: key}]; ok {
				record.Data = append([]byte(nil), record.Data...)
				records = append(records, record)
			}
		}
	}
	return records, nil
}

// Apply performs one validated atomic write through BulkApply.
func (store *Store) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	records, err := store.BulkApply(ctx, []settings.Mutation{mutation})
	if err != nil {
		return settings.Record{}, err
	}
	return records[0], nil
}

// BulkApply atomically validates/CAS-checks and writes 1–1000 unique coordinates, returning
// input order. Cancellation is checked before locking, not during the transaction.
func (store *Store) BulkApply(ctx context.Context, mutations []settings.Mutation) ([]settings.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(mutations) == 0 {
		return nil, fmt.Errorf("%w: bulk size", settings.ErrInvalidMutation)
	}
	if len(mutations) > 1000 {
		return nil, fmt.Errorf("%w: bulk size", settings.ErrInvalidMutation)
	}
	seen := make(map[coordinate]struct{}, len(mutations))
	for _, mutation := range mutations {
		if err := settings.ValidateMutation(mutation); err != nil {
			return nil, err
		}
		coord := coordinate{scope: mutation.Scope, key: mutation.Key}
		if _, ok := seen[coord]; ok {
			return nil, fmt.Errorf("%w: duplicate bulk coordinate", settings.ErrInvalidMutation)
		}
		seen[coord] = struct{}{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, mutation := range mutations {
		coord := coordinate{scope: mutation.Scope, key: mutation.Key}
		if mutation.ExpectedVersion != nil && store.versions[coord] != *mutation.ExpectedVersion {
			return nil, fmt.Errorf("%w: %s at %s", settings.ErrConflict, mutation.Key, mutation.Scope)
		}
	}
	results := make([]settings.Record, 0, len(mutations))
	for _, mutation := range mutations {
		results = append(results, store.applyLocked(mutation))
	}
	return results, nil
}

func (store *Store) applyLocked(mutation settings.Mutation) settings.Record {
	coord := coordinate{scope: mutation.Scope, key: mutation.Key}
	before, present := store.records[coord]
	// Sensitivity is sticky across inherit/clear, so a later direct writer cannot
	// downgrade future history. Existing history is not retroactively scrubbed.
	effectiveSensitive := store.sensitive[coord] || mutation.Sensitive
	store.sensitive[coord] = effectiveSensitive
	store.versions[coord]++
	at := mutation.Change.At
	if at.IsZero() {
		at = store.now().UTC()
	}
	after := settings.Record{
		Scope: mutation.Scope, Key: mutation.Key, Version: store.versions[coord], UpdatedAt: at,
		CodecID: mutation.CodecID, CodecVersion: mutation.CodecVersion,
	}
	switch mutation.Action {
	case settings.ActionSet:
		after.State = settings.StateValue
		after.Data = append([]byte(nil), mutation.Data...)
		store.records[coord] = after
	case settings.ActionClear:
		after.State = settings.StateCleared
		store.records[coord] = after
	case settings.ActionInherit:
		after.State = settings.StateMissing
		store.records[coord] = after
	}
	store.history = append(store.history, settings.ChangeRecord{
		Scope: mutation.Scope, Key: mutation.Key, Action: mutation.Action,
		Version: after.Version, CodecID: mutation.CodecID, CodecVersion: mutation.CodecVersion,
		Before: auditValue(before, present, effectiveSensitive),
		After:  auditValue(after, mutation.Action != settings.ActionInherit, effectiveSensitive),
		Actor:  mutation.Change.Actor, Reason: mutation.Change.Reason, At: at,
	})
	return after
}

func auditValue(record settings.Record, present, sensitive bool) settings.AuditValue {
	value := settings.AuditValue{State: settings.StateMissing}
	if !present {
		return value
	}
	value.State = record.State
	value.Redacted = sensitive && record.State == settings.StateValue
	if !value.Redacted {
		value.Data = append([]byte(nil), record.Data...)
	}
	return value
}

// History copies at most 1–1000 matching events: selects newest appended events, then sorts by
// descending coordinate version, not global timestamp.
func (store *Store) History(ctx context.Context, query settings.HistoryQuery) ([]settings.ChangeRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if query.Limit <= 0 {
		return nil, fmt.Errorf("settings: history limit must be between 1 and 1000")
	}
	if query.Limit > 1000 {
		return nil, fmt.Errorf("settings: history limit must be between 1 and 1000")
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]settings.ChangeRecord, 0, query.Limit)
	for index := len(store.history) - 1; index >= 0 && len(records) < query.Limit; index-- {
		record := store.history[index]
		if record.Scope == query.Scope && (query.Key == "" || record.Key == query.Key) {
			record.Before.Data = append([]byte(nil), record.Before.Data...)
			record.After.Data = append([]byte(nil), record.After.Data...)
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(left, right settings.ChangeRecord) int {
		return cmp.Compare(right.Version, left.Version)
	})
	return records, nil
}

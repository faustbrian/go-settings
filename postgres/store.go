// Package postgres provides the durable PostgreSQL settings provider.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:embed schema.sql
var schema string

// DB is implemented by pgxpool.Pool and permits transaction-scoped tests.
type DB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store persists settings and audit history in one PostgreSQL transaction.
type Store struct{ db DB }

// New constructs a PostgreSQL provider. Call Migrate before serving traffic.
func New(db DB) *Store { return &Store{db: db} }

// Schema returns the idempotent schema owned by this package.
func Schema() string { return schema }

// Migrate creates or upgrades package-owned schema objects.
func (store *Store) Migrate(ctx context.Context) error {
	_, err := store.db.Exec(ctx, schema)
	if err != nil {
		return fmt.Errorf("settings postgres migrate: %w", err)
	}
	return nil
}

func (*Store) Capabilities() settings.Capabilities {
	return settings.Capabilities{
		CompareAndSet: true, AtomicBulk: true, History: true, Snapshots: true,
	}
}

func (store *Store) Get(ctx context.Context, scope settings.Scope, key string) (settings.Record, bool, error) {
	return get(ctx, store.db, scope, key, false)
}

func get(ctx context.Context, db querier, scope settings.Scope, key string, lock bool) (settings.Record, bool, error) {
	if err := ctx.Err(); err != nil {
		return settings.Record{}, false, err
	}
	query := `SELECT state, value, codec_id, codec_version, version, updated_at
FROM settings_values
WHERE scope_kind = $1 AND scope_id = $2 AND key_id = $3`
	if lock {
		query += " FOR UPDATE"
	}
	var record settings.Record
	record.Scope = scope
	record.Key = key
	err := db.QueryRow(ctx, query, scope.Kind, scope.ID, key).Scan(
		&record.State, &record.Data, &record.CodecID, &record.CodecVersion,
		&record.Version, &record.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return settings.Record{}, false, nil
	}
	if err != nil {
		return settings.Record{}, false, fmt.Errorf("settings postgres get: %w", err)
	}
	if record.State == settings.StateMissing {
		return settings.Record{}, false, nil
	}
	return record, true, nil
}

func (store *Store) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	tx, err := store.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("settings postgres begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	records := make([]settings.Record, 0, len(scopes)*len(keys))
	for _, scope := range scopes {
		for _, key := range keys {
			record, ok, getErr := get(ctx, tx, scope, key, false)
			if getErr != nil {
				return nil, getErr
			}
			if ok {
				records = append(records, record)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("settings postgres commit snapshot: %w", err)
	}
	return records, nil
}

func (store *Store) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	records, err := store.BulkApply(ctx, []settings.Mutation{mutation})
	if err != nil {
		return settings.Record{}, err
	}
	return records[0], nil
}

func (store *Store) BulkApply(ctx context.Context, mutations []settings.Mutation) ([]settings.Record, error) {
	if len(mutations) == 0 || len(mutations) > 1000 {
		return nil, fmt.Errorf("%w: bulk size", settings.ErrInvalidMutation)
	}
	type coordinate struct{ scope, key string }
	seen := make(map[coordinate]struct{}, len(mutations))
	for _, mutation := range mutations {
		if err := settings.ValidateMutation(mutation); err != nil {
			return nil, err
		}
		coord := coordinate{scope: mutation.Scope.String(), key: mutation.Key}
		if _, ok := seen[coord]; ok {
			return nil, fmt.Errorf("%w: duplicate bulk coordinate", settings.ErrInvalidMutation)
		}
		seen[coord] = struct{}{}
	}
	tx, err := store.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("settings postgres begin write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	results := make([]settings.Record, 0, len(mutations))
	for _, mutation := range mutations {
		record, applyErr := apply(ctx, tx, mutation)
		if applyErr != nil {
			return nil, applyErr
		}
		results = append(results, record)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("settings postgres commit write: %w", err)
	}
	return results, nil
}

func apply(ctx context.Context, tx pgx.Tx, mutation settings.Mutation) (settings.Record, error) {
	before, present, err := get(ctx, tx, mutation.Scope, mutation.Key, true)
	if err != nil {
		return settings.Record{}, err
	}
	currentVersion := uint64(0)
	if present {
		currentVersion = before.Version
	} else {
		var storedVersion uint64
		err := tx.QueryRow(ctx, `SELECT version FROM settings_values
WHERE scope_kind = $1 AND scope_id = $2 AND key_id = $3 FOR UPDATE`,
			mutation.Scope.Kind, mutation.Scope.ID, mutation.Key).Scan(&storedVersion)
		if err == nil {
			currentVersion = storedVersion
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return settings.Record{}, fmt.Errorf("settings postgres read version: %w", err)
		}
	}
	if mutation.ExpectedVersion != nil && currentVersion != *mutation.ExpectedVersion {
		return settings.Record{}, fmt.Errorf("%w: %s at %s", settings.ErrConflict, mutation.Key, mutation.Scope)
	}
	at := mutation.Change.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	after := settings.Record{
		Scope: mutation.Scope, Key: mutation.Key, Version: currentVersion + 1,
		UpdatedAt: at, CodecID: mutation.CodecID, CodecVersion: mutation.CodecVersion,
	}
	switch mutation.Action {
	case settings.ActionSet:
		after.State = settings.StateValue
		after.Data = append([]byte(nil), mutation.Data...)
	case settings.ActionClear:
		after.State = settings.StateCleared
	case settings.ActionInherit:
		after.State = settings.StateMissing
	}
	_, err = tx.Exec(ctx, `INSERT INTO settings_values
(scope_kind, scope_id, key_id, state, value, codec_id, codec_version, version, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (scope_kind, scope_id, key_id) DO UPDATE SET
state=EXCLUDED.state, value=EXCLUDED.value, codec_id=EXCLUDED.codec_id,
codec_version=EXCLUDED.codec_version, version=EXCLUDED.version, updated_at=EXCLUDED.updated_at`,
		after.Scope.Kind, after.Scope.ID, after.Key, after.State, nullableData(after),
		after.CodecID, after.CodecVersion, after.Version, after.UpdatedAt)
	if err != nil {
		return settings.Record{}, fmt.Errorf("settings postgres write value: %w", err)
	}
	beforeAudit := auditValue(before, present, mutation.Sensitive)
	afterAudit := auditValue(after, mutation.Action != settings.ActionInherit, mutation.Sensitive)
	_, err = tx.Exec(ctx, `INSERT INTO settings_history
(scope_kind,scope_id,key_id,action,version,codec_id,codec_version,
before_state,before_value,before_redacted,after_state,after_value,after_redacted,
actor,reason,changed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		after.Scope.Kind, after.Scope.ID, after.Key, mutation.Action, after.Version,
		after.CodecID, after.CodecVersion, beforeAudit.State, beforeAudit.Data,
		beforeAudit.Redacted, afterAudit.State, afterAudit.Data, afterAudit.Redacted,
		mutation.Change.Actor, mutation.Change.Reason, at)
	if err != nil {
		return settings.Record{}, fmt.Errorf("settings postgres write history: %w", err)
	}
	return after, nil
}

func nullableData(record settings.Record) []byte {
	if record.State != settings.StateValue {
		return nil
	}
	return record.Data
}

func auditValue(record settings.Record, present, sensitive bool) settings.AuditValue {
	if !present {
		return settings.AuditValue{State: settings.StateMissing}
	}
	value := settings.AuditValue{State: record.State, Redacted: sensitive && record.State == settings.StateValue}
	if !value.Redacted {
		value.Data = nullableData(record)
	}
	return value
}

func (store *Store) History(ctx context.Context, query settings.HistoryQuery) ([]settings.ChangeRecord, error) {
	if err := query.Scope.Validate(); err != nil {
		return nil, err
	}
	if query.Limit <= 0 || query.Limit > 1000 {
		return nil, fmt.Errorf("settings: history limit must be between 1 and 1000")
	}
	rows, err := store.db.Query(ctx, `SELECT scope_kind,scope_id,key_id,action,version,
codec_id,codec_version,before_state,before_value,before_redacted,
after_state,after_value,after_redacted,actor,reason,changed_at
FROM settings_history WHERE scope_kind=$1 AND scope_id=$2
AND ($3='' OR key_id=$3) ORDER BY id DESC LIMIT $4`,
		query.Scope.Kind, query.Scope.ID, query.Key, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("settings postgres history: %w", err)
	}
	defer rows.Close()
	records := make([]settings.ChangeRecord, 0, query.Limit)
	for rows.Next() {
		var record settings.ChangeRecord
		if err := rows.Scan(&record.Scope.Kind, &record.Scope.ID, &record.Key,
			&record.Action, &record.Version, &record.CodecID, &record.CodecVersion,
			&record.Before.State, &record.Before.Data, &record.Before.Redacted,
			&record.After.State, &record.After.Data, &record.After.Redacted,
			&record.Actor, &record.Reason, &record.At); err != nil {
			return nil, fmt.Errorf("settings postgres scan history: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("settings postgres history rows: %w", err)
	}
	return records, nil
}

// Completed implements migration.Journal using package-owned checkpoints.
func (store *Store) Completed(ctx context.Context, plan, step string, scope settings.Scope) (bool, error) {
	var completed bool
	err := store.db.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM settings_migrations
WHERE plan_id=$1 AND step_id=$2 AND scope_kind=$3 AND scope_id=$4)`,
		plan, step, scope.Kind, scope.ID).Scan(&completed)
	if err != nil {
		return false, fmt.Errorf("settings postgres read migration checkpoint: %w", err)
	}
	return completed, nil
}

// MarkCompleted implements migration.Journal idempotently.
func (store *Store) MarkCompleted(ctx context.Context, plan, step string, scope settings.Scope, at time.Time) error {
	_, err := store.db.Exec(ctx, `INSERT INTO settings_migrations
(plan_id,step_id,scope_kind,scope_id,completed_at) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (plan_id,step_id,scope_kind,scope_id) DO NOTHING`,
		plan, step, scope.Kind, scope.ID, at)
	if err != nil {
		return fmt.Errorf("settings postgres write migration checkpoint: %w", err)
	}
	return nil
}

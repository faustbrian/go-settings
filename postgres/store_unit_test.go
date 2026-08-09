package postgres_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/postgres"
	"github.com/jackc/pgx/v5"
	pgxmock "github.com/pashagolub/pgxmock/v4"
)

var errDatabase = errors.New("database failure")

func newMockStore(t *testing.T) (pgxmock.PgxPoolIface, *postgres.Store) {
	t.Helper()
	mock, err := pgxmock.NewPool(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherAny))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		mock.Close()
	})
	return mock, postgres.New(mock)
}

func TestStoreDatabaseFailureContracts(t *testing.T) {
	t.Run("migrate", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectExec("schema").WillReturnError(errDatabase)
		if err := store.Migrate(t.Context()); err == nil {
			t.Fatal("migration error hidden")
		}
	})
	t.Run("get query", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectQuery("get").WithArgs(anyArgs(3)...).WillReturnError(errDatabase)
		if _, _, err := store.Get(t.Context(), settings.Global(), "test/key"); err == nil {
			t.Fatal("get error hidden")
		}
	})
	t.Run("get missing and tombstone", func(t *testing.T) {
		mock, store := newMockStore(t)
		columns := []string{"state", "value", "codec_id", "codec_version", "version", "updated_at"}
		mock.ExpectQuery("missing").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows(columns))
		if _, ok, err := store.Get(t.Context(), settings.Global(), "missing"); err != nil || ok {
			t.Fatalf("missing = %v, %v", ok, err)
		}
		mock.ExpectQuery("tombstone").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows(columns).AddRow(
			settings.StateMissing, nil, "string", uint32(1), uint64(1), time.Now()))
		if _, ok, err := store.Get(t.Context(), settings.Global(), "tombstone"); err != nil || ok {
			t.Fatalf("tombstone = %v, %v", ok, err)
		}
	})
	t.Run("bulk begin", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(readOptions()).WillReturnError(errDatabase)
		if _, err := store.BulkGet(t.Context(), nil, nil); err == nil {
			t.Fatal("bulk begin error hidden")
		}
	})
	t.Run("bulk query", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(readOptions())
		mock.ExpectQuery("get").WithArgs(anyArgs(3)...).WillReturnError(errDatabase)
		mock.ExpectRollback()
		if _, err := store.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{"key"}); err == nil {
			t.Fatal("bulk query error hidden")
		}
	})
	t.Run("bulk commit", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(readOptions())
		mock.ExpectCommit().WillReturnError(errDatabase)
		if _, err := store.BulkGet(t.Context(), nil, nil); err == nil {
			t.Fatal("bulk commit error hidden")
		}
	})
	t.Run("write begin", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(writeOptions()).WillReturnError(errDatabase)
		if _, err := store.BulkApply(t.Context(), []settings.Mutation{validPostgresMutation()}); err == nil {
			t.Fatal("write begin error hidden")
		}
	})
	t.Run("write value", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		expectMissingRecord(mock)
		mock.ExpectQuery("version").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows([]string{"version"}))
		mock.ExpectExec("value").WithArgs(anyArgs(9)...).WillReturnError(errDatabase)
		mock.ExpectRollback()
		if _, err := store.Apply(t.Context(), validPostgresMutation()); err == nil {
			t.Fatal("value write error hidden")
		}
	})
	t.Run("write locked read", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		mock.ExpectQuery("record").WithArgs(anyArgs(3)...).WillReturnError(errDatabase)
		mock.ExpectRollback()
		if _, err := store.Apply(t.Context(), validPostgresMutation()); err == nil {
			t.Fatal("locked read error hidden")
		}
	})
	t.Run("write history", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		expectMissingRecord(mock)
		mock.ExpectQuery("version").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows([]string{"version"}))
		mock.ExpectExec("value").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("history").WithArgs(anyArgs(16)...).WillReturnError(errDatabase)
		mock.ExpectRollback()
		if _, err := store.Apply(t.Context(), validPostgresMutation()); err == nil {
			t.Fatal("history write error hidden")
		}
	})
	t.Run("write commit", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		expectMissingRecord(mock)
		mock.ExpectQuery("version").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows([]string{"version"}))
		mock.ExpectExec("value").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("history").WithArgs(anyArgs(16)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit().WillReturnError(errDatabase)
		if _, err := store.Apply(t.Context(), validPostgresMutation()); err == nil {
			t.Fatal("write commit error hidden")
		}
	})
}

func TestStoreSuccessfulProviderContractsWithoutExternalServices(t *testing.T) {
	t.Run("migration and capabilities", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectExec("schema").WillReturnResult(pgxmock.NewResult("CREATE", 0))
		if err := store.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		capabilities := store.Capabilities()
		if !capabilities.CompareAndSet || !capabilities.AtomicBulk || !capabilities.History ||
			!capabilities.Snapshots || capabilities.Subscriptions {
			t.Fatalf("capabilities = %+v", capabilities)
		}
	})

	t.Run("canceled get", func(t *testing.T) {
		_, store := newMockStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := store.Get(ctx, settings.Global(), "key"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled get = %v", err)
		}
	})

	t.Run("bulk retains tombstone", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(readOptions())
		columns := []string{"state", "value", "codec_id", "codec_version", "version", "updated_at"}
		at := time.Unix(1_800_000_000, 0).UTC()
		mock.ExpectQuery("snapshot").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows(columns).AddRow(
			settings.StateMissing, nil, "string", uint32(1), uint64(4), at))
		mock.ExpectCommit()
		records, err := store.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{"fleet/key"})
		if err != nil || len(records) != 1 || records[0].State != settings.StateMissing || records[0].Version != 4 {
			t.Fatalf("tombstone snapshot = (%+v, %v)", records, err)
		}
	})

	t.Run("history and checkpoints", func(t *testing.T) {
		mock, store := newMockStore(t)
		columns := []string{"scope_kind", "scope_id", "key_id", "action", "version", "codec_id", "codec_version",
			"before_state", "before_value", "before_redacted", "after_state", "after_value", "after_redacted",
			"actor", "reason", "changed_at"}
		at := time.Unix(1_800_000_000, 0).UTC()
		mock.ExpectQuery("history").WithArgs(anyArgs(4)...).WillReturnRows(pgxmock.NewRows(columns).AddRow(
			settings.ScopeGlobal, "", "fleet/key", settings.ActionSet, uint64(2), "string", uint32(1),
			settings.StateMissing, nil, false, settings.StateValue, []byte("safe"), false,
			"operator", "change", at))
		history, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1})
		if err != nil || len(history) != 1 || history[0].Version != 2 || string(history[0].After.Data) != "safe" {
			t.Fatalf("history = (%+v, %v)", history, err)
		}
		mock.ExpectQuery("checkpoint").WithArgs(anyArgs(4)...).WillReturnRows(
			pgxmock.NewRows([]string{"exists"}).AddRow(true),
		)
		completed, err := store.Completed(t.Context(), "plan", "step", settings.Global())
		if err != nil || !completed {
			t.Fatalf("completed = (%v, %v)", completed, err)
		}
		mock.ExpectExec("checkpoint").WithArgs(anyArgs(5)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		if err := store.MarkCompleted(t.Context(), "plan", "step", settings.Global(), at); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreCommitsEveryMutationStateAndPreservesVersions(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    settings.Action
		wantState settings.State
		wantData  string
	}{
		{name: "set", action: settings.ActionSet, wantState: settings.StateValue, wantData: "value"},
		{name: "clear", action: settings.ActionClear, wantState: settings.StateCleared},
		{name: "inherit", action: settings.ActionInherit, wantState: settings.StateMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			mock, store := newMockStore(t)
			mock.ExpectBeginTx(writeOptions())
			expectMissingRecord(mock)
			mock.ExpectQuery("version").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows([]string{"version"}))
			mock.ExpectExec("value").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
			mock.ExpectExec("history").WithArgs(anyArgs(16)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
			mock.ExpectCommit()
			mutation := validPostgresMutation()
			mutation.Action = test.action
			if test.action != settings.ActionSet {
				mutation.Data = nil
			}
			record, err := store.Apply(t.Context(), mutation)
			if err != nil || record.State != test.wantState || string(record.Data) != test.wantData || record.Version != 1 {
				t.Fatalf("record = (%+v, %v)", record, err)
			}
		})
	}

	t.Run("existing version", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		columns := []string{"state", "value", "codec_id", "codec_version", "version", "updated_at"}
		mock.ExpectQuery("record").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows(columns).AddRow(
			settings.StateValue, []byte("old"), "string", uint32(1), uint64(7), time.Now()))
		mock.ExpectExec("value").WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("history").WithArgs(anyArgs(16)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectCommit()
		expected := uint64(7)
		mutation := validPostgresMutation()
		mutation.ExpectedVersion = &expected
		record, err := store.Apply(t.Context(), mutation)
		if err != nil || record.Version != 8 || string(record.Data) != "value" {
			t.Fatalf("versioned record = (%+v, %v)", record, err)
		}
	})
}

func TestStoreHistoryAndCheckpointFailureContracts(t *testing.T) {
	t.Run("history validation", func(t *testing.T) {
		_, store := newMockStore(t)
		if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Tenant(""), Limit: 1}); err == nil {
			t.Fatal("invalid history scope accepted")
		}
		if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1001}); err == nil {
			t.Fatal("invalid history limit accepted")
		}
	})
	t.Run("history query", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectQuery("history").WithArgs(anyArgs(4)...).WillReturnError(errDatabase)
		if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1}); err == nil {
			t.Fatal("history query error hidden")
		}
	})
	t.Run("history scan", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectQuery("history").WithArgs(anyArgs(4)...).WillReturnRows(pgxmock.NewRows([]string{"bad"}).AddRow("bad"))
		if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1}); err == nil {
			t.Fatal("history scan error hidden")
		}
	})
	t.Run("history rows", func(t *testing.T) {
		mock, store := newMockStore(t)
		columns := []string{"scope_kind", "scope_id", "key_id", "action", "version", "codec_id", "codec_version",
			"before_state", "before_value", "before_redacted", "after_state", "after_value", "after_redacted",
			"actor", "reason", "changed_at"}
		rows := pgxmock.NewRows(columns).AddRow("global", "", "key", 1, 1, "string", 1,
			0, nil, false, 1, []byte("value"), false, "actor", "reason", time.Now()).RowError(0, errDatabase)
		mock.ExpectQuery("history").WithArgs(anyArgs(4)...).WillReturnRows(rows)
		if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1}); err == nil {
			t.Fatal("history row error hidden")
		}
	})
	t.Run("history close", func(t *testing.T) {
		mock, store := newMockStore(t)
		columns := []string{"scope_kind", "scope_id", "key_id", "action", "version", "codec_id", "codec_version",
			"before_state", "before_value", "before_redacted", "after_state", "after_value", "after_redacted",
			"actor", "reason", "changed_at"}
		rows := pgxmock.NewRows(columns).CloseError(errDatabase)
		mock.ExpectQuery("history").WithArgs(anyArgs(4)...).WillReturnRows(rows)
		if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1}); err == nil {
			t.Fatal("history close error hidden")
		}
	})
	t.Run("checkpoint read", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectQuery("checkpoint").WithArgs(anyArgs(4)...).WillReturnError(errDatabase)
		if _, err := store.Completed(t.Context(), "plan", "step", settings.Global()); err == nil {
			t.Fatal("checkpoint read error hidden")
		}
	})
	t.Run("checkpoint write", func(t *testing.T) {
		mock, store := newMockStore(t)
		mock.ExpectExec("checkpoint").WithArgs(anyArgs(5)...).WillReturnError(errDatabase)
		if err := store.MarkCompleted(t.Context(), "plan", "step", settings.Global(), time.Now()); err == nil {
			t.Fatal("checkpoint write error hidden")
		}
	})
}

func TestStoreWriteValidationAndVersionFailures(t *testing.T) {
	_, store := newMockStore(t)
	if _, err := store.BulkApply(t.Context(), nil); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("empty bulk = %v", err)
	}
	tooMany := make([]settings.Mutation, 1001)
	if _, err := store.BulkApply(t.Context(), tooMany); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("oversized bulk = %v", err)
	}
	mutation := validPostgresMutation()
	invalid := mutation
	invalid.Key = ""
	if _, err := store.BulkApply(t.Context(), []settings.Mutation{invalid}); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("invalid mutation = %v", err)
	}
	if _, err := store.BulkApply(t.Context(), []settings.Mutation{mutation, mutation}); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("duplicate bulk = %v", err)
	}

	t.Run("stored version query", func(t *testing.T) {
		mock, nestedStore := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		expectMissingRecord(mock)
		mock.ExpectQuery("version").WithArgs(anyArgs(3)...).WillReturnError(errDatabase)
		mock.ExpectRollback()
		if _, err := nestedStore.Apply(t.Context(), mutation); err == nil {
			t.Fatal("version query error hidden")
		}
	})
	t.Run("conflict", func(t *testing.T) {
		mock, nestedStore := newMockStore(t)
		mock.ExpectBeginTx(writeOptions())
		expectMissingRecord(mock)
		mock.ExpectQuery("version").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows([]string{"version"}).AddRow(uint64(2)))
		mock.ExpectRollback()
		expected := uint64(1)
		mutation.ExpectedVersion = &expected
		if _, err := nestedStore.Apply(t.Context(), mutation); !errors.Is(err, settings.ErrConflict) {
			t.Fatalf("version conflict = %v", err)
		}
	})
}

func TestStoreUsesLockedReadsAndPersistsEffectiveAuditState(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		mock.Close()
	})
	store := postgres.New(mock)
	mock.ExpectBeginTx(writeOptions())
	mock.ExpectQuery(`(?s)SELECT state, value, codec_id, codec_version, version, updated_at.*FOR UPDATE`).
		WithArgs(settings.ScopeGlobal, "", "test/key").
		WillReturnRows(pgxmock.NewRows([]string{"state", "value", "codec_id", "codec_version", "version", "updated_at"}))
	mock.ExpectQuery(`(?s)SELECT version FROM settings_values.*FOR UPDATE`).
		WithArgs(settings.ScopeGlobal, "", "test/key").
		WillReturnRows(pgxmock.NewRows([]string{"version"}))
	mock.ExpectExec(`INSERT INTO settings_values`).WithArgs(anyArgs(9)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`INSERT INTO settings_history`).WithArgs(
		settings.ScopeGlobal, "", "test/key", settings.ActionSet, uint64(1), "string", uint32(1),
		settings.StateMissing, pgxmock.AnyArg(), false,
		settings.StateValue, []byte("value"), false,
		"test", "unit", pgxmock.AnyArg(),
	).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()
	if _, err := store.Apply(t.Context(), validPostgresMutation()); err != nil {
		t.Fatal(err)
	}
}

func TestStoreAcceptsExactBulkAndHistoryLimits(t *testing.T) {
	t.Run("bulk", func(t *testing.T) {
		mock, store := newMockStore(t)
		mutations := make([]settings.Mutation, 1_000)
		for index := range mutations {
			mutations[index] = validPostgresMutation()
			mutations[index].Key = "bulk/" + strconv.Itoa(index)
		}
		mock.ExpectBeginTx(writeOptions()).WillReturnError(errDatabase)
		if _, err := store.BulkApply(t.Context(), mutations); !errors.Is(err, errDatabase) {
			t.Fatalf("exact 1000 mutation bulk error = %v", err)
		}
	})
	t.Run("history", func(t *testing.T) {
		mock, store := newMockStore(t)
		columns := []string{"scope_kind", "scope_id", "key_id", "action", "version", "codec_id", "codec_version",
			"before_state", "before_value", "before_redacted", "after_state", "after_value", "after_redacted",
			"actor", "reason", "changed_at"}
		mock.ExpectQuery("history").WithArgs(settings.ScopeGlobal, "", "", 1_000).
			WillReturnRows(pgxmock.NewRows(columns))
		if records, err := store.History(t.Context(), settings.HistoryQuery{
			Scope: settings.Global(), Limit: 1_000,
		}); err != nil || len(records) != 0 {
			t.Fatalf("exact 1000 history limit = (%d records, %v)", len(records), err)
		}
		for _, limit := range []int{0, 1_001} {
			if _, err := store.History(t.Context(), settings.HistoryQuery{
				Scope: settings.Global(), Limit: limit,
			}); err == nil || err.Error() != "settings: history limit must be between 1 and 1000" {
				t.Errorf("invalid history limit %d = %v", limit, err)
			}
		}
	})
}

func validPostgresMutation() settings.Mutation {
	return settings.Mutation{
		Scope: settings.Global(), Key: "test/key", Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1,
		Change: settings.Change{Actor: "test", Reason: "unit"},
	}
}

func expectMissingRecord(mock pgxmock.PgxPoolIface) {
	mock.ExpectQuery("record").WithArgs(anyArgs(3)...).WillReturnRows(pgxmock.NewRows(
		[]string{"state", "value", "codec_id", "codec_version", "version", "updated_at"}))
}

func readOptions() pgx.TxOptions {
	return pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
}

func writeOptions() pgx.TxOptions {
	return pgx.TxOptions{IsoLevel: pgx.Serializable}
}

func anyArgs(count int) []any {
	args := make([]any, count)
	for index := range args {
		args[index] = pgxmock.AnyArg()
	}
	return args
}

var _ postgres.DB = pgxmock.PgxPoolIface(nil)

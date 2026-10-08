package postgres_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	settings "github.com/faustbrian/go-settings/v2"
	"github.com/faustbrian/go-settings/v2/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDriverCancellationReleasesConnectionWithoutPartialWrite(t *testing.T) {
	pool, admin, application := driverFixture(t, 1)
	store := postgres.New(pool)
	mutation := validPostgresMutation()
	if _, err := store.Apply(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	lock := lockDriverRecord(t, admin)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	completed := make(chan error, 1)
	mutation.Data = []byte("canceled")
	go func() { _, err := store.Apply(ctx, mutation); completed <- err }()
	waitDriverLocks(t, admin, application, 1)
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked write did not observe cancellation")
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, done := context.WithTimeout(t.Context(), 5*time.Second)
	defer done()
	if _, err := pool.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("connection unavailable after cancellation: %v", err)
	}
	assertDriverState(t, admin, 1, "value", 1)
}

func TestDriverConcurrentCompareAndSetKeepsHistoryAtomic(t *testing.T) {
	pool, admin, application := driverFixture(t, 2)
	store := postgres.New(pool)
	mutation := validPostgresMutation()
	if _, err := store.Apply(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	lock := lockDriverRecord(t, admin)
	type outcome struct {
		value string
		err   error
	}
	completed := make(chan outcome, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for _, value := range []string{"first", "second"} {
		go func() {
			candidate := mutation
			candidate.Data = []byte(value)
			version := uint64(1)
			candidate.ExpectedVersion = &version
			_, err := store.Apply(ctx, candidate)
			completed <- outcome{value, err}
		}()
	}
	// Both transactions must reach PostgreSQL's row lock before either writes.
	waitDriverLocks(t, admin, application, 2)
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	winner := ""
	for range 2 {
		select {
		case result := <-completed:
			if result.err == nil {
				if winner != "" {
					t.Fatal("both writes accepted the same expected version")
				}
				winner = result.value
				continue
			}
			var pgError *pgconn.PgError
			if !errors.Is(result.err, settings.ErrConflict) &&
				(!errors.As(result.err, &pgError) || pgError.Code != "40001") {
				t.Fatalf("losing write = %v", result.err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent writes did not settle")
		}
	}
	if winner == "" {
		t.Fatal("neither write committed")
	}
	assertDriverState(t, admin, 2, winner, 2)
	var matching int
	if err := admin.QueryRow(t.Context(), `SELECT count(*) FROM settings_history
WHERE version = 2 AND after_value = $1`, []byte(winner)).Scan(&matching); err != nil || matching != 1 {
		t.Fatalf("matching committed history = %d, %v", matching, err)
	}
}

func driverFixture(t *testing.T, maxConnections int32) (*pgxpool.Pool, *pgx.Conn, string) {
	t.Helper()
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		t.Skip("POSTGRES_URL is not set")
	}
	admin, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = admin.Close(ctx)
	})
	schema := "settings_driver_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop driver fixture: %v", err)
		}
	})
	if _, err := admin.Exec(t.Context(), "SET search_path TO "+identifier); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = maxConnections
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.RuntimeParams["application_name"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() { pool.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(5 * time.Second):
			t.Error("driver pool did not close without terminating backends")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = admin.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1", schema)
			select {
			case <-closed:
			case <-ctx.Done():
				t.Error("driver pool cleanup failed")
			}
		}
	})
	if err := postgres.New(pool).Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return pool, admin, schema
}

func lockDriverRecord(t *testing.T, admin *pgx.Conn) pgx.Tx {
	t.Helper()
	tx, err := admin.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	})
	if _, err := tx.Exec(t.Context(), "SELECT 1 FROM settings_values FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	return tx
}

func waitDriverLocks(t *testing.T, admin *pgx.Conn, application string, expected int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		// The lock owner remains in a transaction; refresh its statistics view.
		if _, err := admin.Exec(ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE application_name = $1 AND wait_event_type = 'Lock'`, application).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == expected {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("waiting for %d blocked transactions, observed %d", expected, count)
		}
	}
}

func assertDriverState(t *testing.T, admin *pgx.Conn, version uint64, value string, history int) {
	t.Helper()
	var actualVersion uint64
	var actualValue []byte
	var actualHistory int
	if err := admin.QueryRow(t.Context(), "SELECT version, value FROM settings_values").Scan(&actualVersion, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := admin.QueryRow(t.Context(), "SELECT count(*) FROM settings_history").Scan(&actualHistory); err != nil {
		t.Fatal(err)
	}
	if actualVersion != version || string(actualValue) != value || actualHistory != history {
		t.Fatalf("persisted state = version %d, value %q, history %d", actualVersion, actualValue, actualHistory)
	}
}

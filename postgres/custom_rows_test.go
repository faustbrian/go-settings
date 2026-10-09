package postgres_test

import (
	"context"
	"errors"
	"testing"

	settings "github.com/faustbrian/go-settings/v3"
	"github.com/faustbrian/go-settings/v3/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCustomRowsTypeMapSupportsHistory(t *testing.T) {
	typeMap := pgtype.NewMap()
	rows := &customHistoryRows{typeMap: typeMap}
	db := &customHistoryDB{rows: rows}
	store := postgres.New(db)
	records, err := store.History(t.Context(), settings.HistoryQuery{
		Scope: settings.Global(), Limit: 1,
	})
	if err != nil || len(records) != 0 || db.queries != 1 || !rows.closed {
		t.Fatalf("custom history: records=%d queries=%d closed=%t err=%v",
			len(records), db.queries, rows.closed, err)
	}
	if rows.Conn() != nil || rows.TypeMap() != typeMap {
		t.Fatal("custom rows must expose their decoding map without a connection")
	}
}

type customHistoryDB struct {
	rows    *customHistoryRows
	queries int
}

func (db *customHistoryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	db.queries++
	return db.rows, nil
}

func (*customHistoryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (*customHistoryDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return customUnexpectedRow{}
}

func (*customHistoryDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return nil, errors.New("unexpected BeginTx")
}

type customUnexpectedRow struct{}

func (customUnexpectedRow) Scan(...any) error { return errors.New("unexpected QueryRow.Scan") }

type customHistoryRows struct {
	typeMap *pgtype.Map
	closed  bool
}

func (rows *customHistoryRows) Close()                                  { rows.closed = true }
func (*customHistoryRows) Err() error                                   { return nil }
func (*customHistoryRows) CommandTag() pgconn.CommandTag                { return pgconn.NewCommandTag("SELECT 0") }
func (*customHistoryRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*customHistoryRows) Next() bool                                   { return false }
func (*customHistoryRows) Scan(...any) error                            { return errors.New("no current row") }
func (*customHistoryRows) Values() ([]any, error)                       { return nil, errors.New("no current row") }
func (*customHistoryRows) RawValues() [][]byte                          { return nil }
func (*customHistoryRows) Conn() *pgx.Conn                              { return nil }
func (rows *customHistoryRows) TypeMap() *pgtype.Map                    { return rows.typeMap }

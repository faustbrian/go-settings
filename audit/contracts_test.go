package audit_test

import (
	"context"
	"errors"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/audit"
)

type historyReader struct {
	records []settings.ChangeRecord
	err     error
}

func (reader historyReader) History(context.Context, settings.HistoryQuery) ([]settings.ChangeRecord, error) {
	return reader.records, reader.err
}

func TestReadRejectsUninterpretableHistoryAndCopiesPublicValues(t *testing.T) {
	t.Parallel()

	registry := settings.NewRegistry()
	key := settings.NewKey("audit", "value", settings.StringCodec{})
	if err := registry.Register(key); err != nil {
		t.Fatal(err)
	}
	query := settings.HistoryQuery{Scope: settings.Global(), Limit: 1}
	if _, err := audit.Read(t.Context(), historyReader{err: errors.New("read")}, registry, query); err == nil {
		t.Fatal("reader error was hidden")
	}
	if _, err := audit.Read(t.Context(), historyReader{records: []settings.ChangeRecord{{Key: "unknown"}}}, registry, query); err == nil {
		t.Fatal("unknown history key accepted")
	}
	if _, err := audit.Read(t.Context(), historyReader{records: []settings.ChangeRecord{{
		Key: key.StableID(), CodecID: "other",
	}}}, registry, query); err == nil {
		t.Fatal("incompatible history codec accepted")
	}
	source := []byte("value")
	records, err := audit.Read(t.Context(), historyReader{records: []settings.ChangeRecord{{
		Key: key.StableID(), CodecID: key.CodecID(),
		Before: settings.AuditValue{State: settings.StateValue, Data: source},
		After:  settings.AuditValue{State: settings.StateValue, Data: source},
	}}}, registry, query)
	if err != nil {
		t.Fatal(err)
	}
	records[0].After.Data[0] = 'X'
	if string(source) != "value" {
		t.Fatal("audit result aliases provider memory")
	}
}

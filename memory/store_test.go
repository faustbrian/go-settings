package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestStoreBoundariesCopiesAndHistory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store := memory.NewWithClock(func() time.Time { return now })
	change := settings.Change{Actor: "operator", Reason: "test"}
	mutation := settings.Mutation{
		Scope: settings.Global(), Key: "test/key", Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1, Change: change,
	}
	created, err := store.Apply(t.Context(), mutation)
	if err != nil || !created.UpdatedAt.Equal(now) {
		t.Fatalf("created = %#v, %v", created, err)
	}
	records, err := store.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{"missing", mutation.Key})
	if err != nil || len(records) != 1 {
		t.Fatalf("bulk get = %#v, %v", records, err)
	}
	records[0].Data[0] = 'X'
	stored, _, _ := store.Get(t.Context(), settings.Global(), mutation.Key)
	if string(stored.Data) != "value" {
		t.Fatal("bulk result aliased provider data")
	}

	mutation.Action = settings.ActionClear
	mutation.Data = nil
	if _, err := store.Apply(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	mutation.Action = settings.ActionInherit
	if _, err := store.Apply(t.Context(), mutation); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(t.Context(), settings.Global(), mutation.Key); err != nil || ok {
		t.Fatalf("inherited get = %v, %v", ok, err)
	}
	history, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 10})
	if err != nil || len(history) != 3 || history[0].Action != settings.ActionInherit {
		t.Fatalf("history = %#v, %v", history, err)
	}
	history[2].After.Data[0] = 'X'
	historyAgain, _ := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Key: mutation.Key, Limit: 1})
	if len(historyAgain) != 1 {
		t.Fatal("history key filter failed")
	}
	if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 0}); err == nil {
		t.Fatal("history accepted zero limit")
	}
}

func TestStoreRejectsInvalidBulkAndCancellation(t *testing.T) {
	t.Parallel()

	store := memory.New()
	change := settings.Change{Actor: "operator", Reason: "test"}
	mutation := settings.Mutation{
		Scope: settings.Global(), Key: "test/key", Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1, Change: change,
	}
	if _, err := store.BulkApply(t.Context(), nil); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("empty bulk = %v", err)
	}
	tooMany := make([]settings.Mutation, 1001)
	if _, err := store.BulkApply(t.Context(), tooMany); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("oversized bulk = %v", err)
	}
	if _, err := store.BulkApply(t.Context(), []settings.Mutation{mutation, mutation}); !errors.Is(err, settings.ErrInvalidMutation) {
		t.Fatalf("duplicate bulk = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.BulkGet(canceled, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bulk get = %v", err)
	}
	if _, err := store.BulkApply(canceled, []settings.Mutation{mutation}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bulk apply = %v", err)
	}
	if _, err := store.History(canceled, settings.HistoryQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled history = %v", err)
	}
}

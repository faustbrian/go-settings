package memory_test

import (
	"errors"
	"strconv"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
)

func TestStoreAcceptsExactBulkAndHistoryLimits(t *testing.T) {
	t.Parallel()

	store := memory.New()
	mutations := make([]settings.Mutation, 1_000)
	for index := range mutations {
		mutations[index] = settings.Mutation{
			Scope: settings.Global(), Key: "bulk/" + strconv.Itoa(index), Action: settings.ActionSet,
			Data: []byte("value"), CodecID: "string", CodecVersion: 1,
			Change: settings.Change{Actor: "operator", Reason: "exact bulk boundary"},
		}
	}
	records, err := store.BulkApply(t.Context(), mutations)
	if err != nil || len(records) != 1_000 {
		t.Fatalf("exact 1000 mutation bulk = (%d records, %v)", len(records), err)
	}
	history, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1_000})
	if err != nil || len(history) != 1_000 || history[0].Version != 1 || history[999].Version != 1 {
		t.Fatalf("exact 1000 history limit = (%d records, %v)", len(history), err)
	}
	if _, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1_001}); err == nil {
		t.Fatal("history accepted limit above 1000")
	}
}

func TestStoreHistoryFiltersAndRedactionAreStateSpecific(t *testing.T) {
	t.Parallel()

	store := memory.New()
	change := settings.Change{Actor: "operator", Reason: "audit contract"}
	apply := func(scope settings.Scope, key string, sensitive bool, action settings.Action, data string) {
		t.Helper()
		_, err := store.Apply(t.Context(), settings.Mutation{
			Scope: scope, Key: key, Action: action, Data: []byte(data), Sensitive: sensitive,
			CodecID: "string", CodecVersion: 1, Change: change,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	apply(settings.Global(), "audit/secret", true, settings.ActionSet, "secret")
	apply(settings.Global(), "audit/secret", true, settings.ActionClear, "")
	apply(settings.Global(), "audit/secret", true, settings.ActionInherit, "")
	apply(settings.Global(), "audit/public", false, settings.ActionSet, "public")
	apply(settings.Tenant("other"), "audit/secret", false, settings.ActionSet, "other")

	secret, err := store.History(t.Context(), settings.HistoryQuery{
		Scope: settings.Global(), Key: "audit/secret", Limit: 10,
	})
	if err != nil || len(secret) != 3 {
		t.Fatalf("secret history = (%+v, %v)", secret, err)
	}
	if secret[2].After.State != settings.StateValue || !secret[2].After.Redacted || len(secret[2].After.Data) != 0 {
		t.Fatalf("secret set audit = %+v", secret[2].After)
	}
	if !secret[1].Before.Redacted || secret[1].After.State != settings.StateCleared ||
		secret[1].After.Redacted || len(secret[1].After.Data) != 0 {
		t.Fatalf("secret clear audit = (%+v, %+v)", secret[1].Before, secret[1].After)
	}
	if secret[0].Before.State != settings.StateCleared || secret[0].Before.Redacted ||
		secret[0].After.State != settings.StateMissing || secret[0].After.Redacted {
		t.Fatalf("secret inherit audit = (%+v, %+v)", secret[0].Before, secret[0].After)
	}

	global, err := store.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 10})
	if err != nil || len(global) != 4 {
		t.Fatalf("global unfiltered history = (%d records, %v)", len(global), err)
	}
	var public *settings.ChangeRecord
	for _, record := range global {
		if record.Scope != settings.Global() {
			t.Fatalf("history leaked another scope: %+v", record.Scope)
		}
		if record.Key == "audit/public" {
			candidate := record
			public = &candidate
		}
	}
	if public == nil || public.After.Redacted || string(public.After.Data) != "public" {
		t.Fatalf("public audit = %+v", public)
	}
}

func TestStoreRejectsAConflictingExactVersionWithoutMutation(t *testing.T) {
	t.Parallel()

	store := memory.New()
	expected := uint64(1)
	mutation := settings.Mutation{
		Scope: settings.Global(), Key: "conflict/key", Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1,
		ExpectedVersion: &expected,
		Change:          settings.Change{Actor: "operator", Reason: "conflict"},
	}
	if _, err := store.Apply(t.Context(), mutation); !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("missing version conflict = %v", err)
	}
	if _, ok, err := store.Get(t.Context(), settings.Global(), mutation.Key); err != nil || ok {
		t.Fatalf("conflicting mutation became visible = (%v, %v)", ok, err)
	}
}

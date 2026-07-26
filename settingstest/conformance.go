// Package settingstest provides a reusable provider conformance suite.
package settingstest

import (
	"context"
	"errors"
	"testing"

	settings "github.com/faustbrian/golib/pkg/settings"
)

// Factory returns an isolated provider for one conformance test.
type Factory func(*testing.T) settings.Provider

// RunProvider verifies the behavior shared by every durable provider.
func RunProvider(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("optimistic concurrency", func(t *testing.T) {
		provider := factory(t)
		mutation := validMutation()
		created, err := provider.Apply(t.Context(), mutation)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		expected := created.Version
		mutation.ExpectedVersion = &expected
		if _, err := provider.Apply(t.Context(), mutation); err != nil {
			t.Fatalf("compare and set: %v", err)
		}
		if _, err := provider.Apply(t.Context(), mutation); !errors.Is(err, settings.ErrConflict) {
			t.Fatalf("stale compare and set = %v, want conflict", err)
		}
	})

	t.Run("atomic bulk", func(t *testing.T) {
		provider := factory(t)
		first := validMutation()
		second := validMutation()
		second.Key = "test/second"
		wrong := uint64(9)
		second.ExpectedVersion = &wrong
		if _, err := provider.BulkApply(t.Context(), []settings.Mutation{first, second}); !errors.Is(err, settings.ErrConflict) {
			t.Fatalf("bulk error = %v, want conflict", err)
		}
		if _, ok, err := provider.Get(t.Context(), first.Scope, first.Key); err != nil || ok {
			t.Fatalf("failed bulk partially committed: ok=%v err=%v", ok, err)
		}
	})

	t.Run("audit metadata and validation", func(t *testing.T) {
		provider := factory(t)
		mutation := validMutation()
		created, err := provider.Apply(t.Context(), mutation)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		history, err := provider.History(t.Context(), settings.HistoryQuery{
			Scope: mutation.Scope, Key: mutation.Key, Limit: 10,
		})
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(history) != 1 || history[0].Actor != mutation.Change.Actor ||
			history[0].Reason != mutation.Change.Reason || history[0].Version != created.Version {
			t.Fatalf("history = %#v", history)
		}
		mutation.Change = settings.Change{}
		if _, err := provider.Apply(t.Context(), mutation); !errors.Is(err, settings.ErrInvalidChange) {
			t.Fatalf("missing change error = %v, want ErrInvalidChange", err)
		}
	})

	t.Run("provider boundary validates scope", func(t *testing.T) {
		provider := factory(t)
		mutation := validMutation()
		mutation.Scope = settings.Tenant("")
		if _, err := provider.Apply(t.Context(), mutation); !errors.Is(err, settings.ErrInvalidScope) {
			t.Fatalf("invalid scope error = %v, want ErrInvalidScope", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		provider := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := provider.Get(ctx, settings.Global(), "test/key"); !errors.Is(err, context.Canceled) {
			t.Fatalf("get error = %v, want canceled", err)
		}
	})
}

func validMutation() settings.Mutation {
	return settings.Mutation{
		Scope: settings.Global(), Key: "test/key", Action: settings.ActionSet,
		Data: []byte("value"), CodecID: "string", CodecVersion: 1,
		Change: settings.Change{Actor: "conformance", Reason: "test"},
	}
}

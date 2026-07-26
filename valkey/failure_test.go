package valkey_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	cache "github.com/faustbrian/golib/pkg/settings/valkey"
)

type failingDurable struct {
	settings.Provider
	getErr, bulkGetErr, applyErr, bulkApplyErr error
}

func (provider failingDurable) Get(context.Context, settings.Scope, string) (settings.Record, bool, error) {
	return settings.Record{}, false, provider.getErr
}
func (provider failingDurable) BulkGet(context.Context, []settings.Scope, []string) ([]settings.Record, error) {
	return nil, provider.bulkGetErr
}
func (provider failingDurable) Apply(context.Context, settings.Mutation) (settings.Record, error) {
	return settings.Record{}, provider.applyErr
}
func (provider failingDurable) BulkApply(context.Context, []settings.Mutation) ([]settings.Record, error) {
	return nil, provider.bulkApplyErr
}

func TestCacheDurableAndTransportFailureContracts(t *testing.T) {
	t.Parallel()

	failure := errors.New("failure")
	base := memory.New()
	transport := newFakeTransport()
	key := settings.NewKey("failure", "value", settings.StringCodec{})
	change := settings.Change{Actor: "operator", Reason: "test"}
	mutation, _ := settings.PrepareSet(settings.Global(), key, "value", nil, change)

	provider := cache.New(failingDurable{Provider: base, getErr: failure, bulkGetErr: failure,
		applyErr: failure, bulkApplyErr: failure}, transport, cache.Config{ReadPolicy: cache.Strong})
	if _, _, err := provider.Get(t.Context(), settings.Global(), key.StableID()); err == nil {
		t.Fatal("durable get error hidden")
	}
	if _, err := provider.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{key.StableID()}); err == nil {
		t.Fatal("durable bulk get error hidden")
	}
	if _, err := provider.Apply(t.Context(), mutation); err == nil {
		t.Fatal("durable apply error hidden")
	}
	if _, err := provider.BulkApply(t.Context(), []settings.Mutation{mutation}); err == nil {
		t.Fatal("durable bulk apply error hidden")
	}

	transport.getErr = failure
	failClosed := cache.New(base, transport, cache.Config{
		ReadPolicy: cache.BoundedStale, OutagePolicy: cache.FailClosed,
	})
	if _, _, err := failClosed.Get(t.Context(), settings.Global(), key.StableID()); err == nil {
		t.Fatal("fail-closed cache get error hidden")
	}
	transport.getErr = nil
	_, _ = settings.Set(t.Context(), base, settings.Global(), key, "value", change)
	transport.setErr = failure
	if _, err := failClosed.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{key.StableID()}); err == nil {
		t.Fatal("bulk cache fill error hidden")
	}
	transport.setErr = nil
	transport.publishErr = failure
	if _, err := failClosed.BulkApply(t.Context(), []settings.Mutation{mutation}); err == nil {
		t.Fatal("bulk invalidation error hidden")
	}
}

func TestCacheMalformedRecordContractsAndTransportWatcherError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := memory.New()
	transport := newFakeTransport()
	provider := cache.New(base, transport, cache.Config{Prefix: "malformed", TTL: time.Minute})
	key := settings.NewKey("malformed", "value", settings.StringCodec{})
	change := settings.Change{Actor: "operator", Reason: "test"}
	_, _ = settings.Set(ctx, base, settings.Global(), key, "durable", change)
	if _, _, err := provider.Get(ctx, settings.Global(), key.StableID()); err != nil {
		t.Fatal(err)
	}

	invalidRecords := []settings.Record{
		{Scope: settings.Tenant("other"), Key: key.StableID(), State: settings.StateValue,
			Data: []byte("value"), CodecID: "string", CodecVersion: 1, Version: 1},
		{Scope: settings.Global(), Key: key.StableID(), State: settings.StateValue,
			Data: []byte("value"), CodecID: "string", CodecVersion: 1},
		{Scope: settings.Global(), Key: key.StableID(), State: settings.StateMissing,
			CodecID: "string", CodecVersion: 1, Version: 1},
		{Scope: settings.Global(), Key: key.StableID(), State: settings.StateValue,
			Data: make([]byte, 1<<20+1), CodecID: "string", CodecVersion: 1, Version: 1},
	}
	for _, invalid := range invalidRecords {
		data, err := json.Marshal(invalid)
		if err != nil {
			t.Fatal(err)
		}
		setOnlyCacheValue(transport, data)
		if _, ok, err := provider.Get(ctx, settings.Global(), key.StableID()); err != nil || !ok {
			t.Fatalf("invalid cache fallback = %v, %v", ok, err)
		}
	}
	setOnlyCacheValue(transport, make([]byte, 2<<20+1))
	if _, ok, err := provider.Get(ctx, settings.Global(), key.StableID()); err != nil || !ok {
		t.Fatalf("oversized cache fallback = %v, %v", ok, err)
	}

	watchContext, cancel := context.WithCancel(ctx)
	events, errs, err := provider.Watch(watchContext, 1)
	if err != nil {
		t.Fatal(err)
	}
	transport.subscribeErrors <- errors.New("subscription failed")
	if err := <-errs; err == nil {
		t.Fatal("transport watcher error hidden")
	}
	cancel()
	for range events {
	}
}

func setOnlyCacheValue(transport *fakeTransport, data []byte) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	for key := range transport.values {
		transport.values[key] = data
		return
	}
}

func TestWatchClosesWhenTransportChannelsClose(t *testing.T) {
	t.Parallel()

	messageTransport := newFakeTransport()
	messageProvider := cache.New(memory.New(), messageTransport, cache.Config{})
	events, _, err := messageProvider.Watch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	close(messageTransport.messages)
	if _, open := <-events; open {
		t.Fatal("events remained open after message transport closed")
	}

	errorTransport := newFakeTransport()
	errorProvider := cache.New(memory.New(), errorTransport, cache.Config{})
	events, _, err = errorProvider.Watch(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	close(errorTransport.subscribeErrors)
	if _, open := <-events; open {
		t.Fatal("events remained open after error transport closed")
	}
}

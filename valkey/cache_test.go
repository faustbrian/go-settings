package valkey_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
	"github.com/faustbrian/golib/pkg/settings/memory"
	"github.com/faustbrian/golib/pkg/settings/settingstest"
	cache "github.com/faustbrian/golib/pkg/settings/valkey"
)

type fakeTransport struct {
	mu              sync.Mutex
	values          map[string][]byte
	getErr          error
	setErr          error
	deleteErr       error
	publishErr      error
	messages        chan []byte
	subscribeErrors chan error
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		values: make(map[string][]byte), messages: make(chan []byte, 16),
		subscribeErrors: make(chan error, 1),
	}
}

func TestProviderConformance(t *testing.T) {
	settingstest.RunProvider(t, func(*testing.T) settings.Provider {
		return cache.New(memory.New(), newFakeTransport(), cache.Config{
			ReadPolicy: cache.Strong, OutagePolicy: cache.FailClosed,
		})
	})
}

func (transport *fakeTransport) Get(_ context.Context, key string) ([]byte, bool, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.getErr != nil {
		return nil, false, transport.getErr
	}
	value, ok := transport.values[key]
	return append([]byte(nil), value...), ok, nil
}
func (transport *fakeTransport) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.setErr != nil {
		return transport.setErr
	}
	transport.values[key] = append([]byte(nil), value...)
	return nil
}
func (transport *fakeTransport) Delete(_ context.Context, key string) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.deleteErr != nil {
		return transport.deleteErr
	}
	delete(transport.values, key)
	return nil
}
func (transport *fakeTransport) Publish(_ context.Context, _ string, value []byte) error {
	if transport.publishErr != nil {
		return transport.publishErr
	}
	select {
	case transport.messages <- append([]byte(nil), value...):
	default:
	}
	return nil
}

func TestCacheStrongBulkHistoryAndFailureContracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	durable := memory.New()
	transport := newFakeTransport()
	provider := cache.New(durable, transport, cache.Config{
		ReadPolicy: cache.Strong, OutagePolicy: cache.FailClosed,
	})
	if !provider.Capabilities().Subscriptions {
		t.Fatal("subscription capability absent")
	}
	key := settings.NewKey("cache", "value", settings.StringCodec{})
	change := settings.Change{Actor: "operator", Reason: "test"}
	mutation, err := settings.PrepareSet(settings.Global(), key, "value", nil, change)
	if err != nil {
		t.Fatal(err)
	}
	records, err := provider.BulkApply(ctx, []settings.Mutation{mutation})
	if err != nil || len(records) != 1 {
		t.Fatalf("bulk apply = %#v, %v", records, err)
	}
	if _, err := provider.BulkGet(ctx, []settings.Scope{settings.Global()}, []string{key.StableID()}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.History(ctx, settings.HistoryQuery{Scope: settings.Global(), Limit: 10}); err != nil {
		t.Fatal(err)
	}

	transport.mu.Lock()
	transport.setErr = errors.New("set unavailable")
	transport.mu.Unlock()
	if _, _, err := provider.Get(ctx, settings.Global(), key.StableID()); err == nil {
		t.Fatal("strong cache fill failure was hidden")
	}
	transport.mu.Lock()
	transport.setErr = nil
	transport.publishErr = errors.New("publish unavailable")
	transport.mu.Unlock()
	record, err := settings.Set(ctx, provider, settings.Global(), key, "next", change)
	var cacheErr *cache.CacheError
	if !errors.As(err, &cacheErr) || !cacheErr.Committed || record.Version == 0 ||
		cacheErr.Error() == "" || cacheErr.Unwrap() == nil {
		t.Fatalf("cache error = %#v, record = %#v", err, record)
	}
	transport.mu.Lock()
	transport.publishErr = nil
	transport.deleteErr = errors.New("delete unavailable")
	transport.mu.Unlock()
	if _, err := settings.Inherit(ctx, provider, settings.Global(), key, change); err == nil {
		t.Fatal("read-after-write delete failure was hidden")
	}
}

func TestCacheRejectsMalformedEntriesAndWatcherInputs(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	durable := memory.New()
	provider := cache.New(durable, transport, cache.Config{Prefix: "malformed", TTL: time.Minute})
	key := settings.NewKey("cache", "value", settings.StringCodec{})
	change := settings.Change{Actor: "operator", Reason: "test"}
	if _, err := settings.Set(context.Background(), durable, settings.Global(), key, "durable", change); err != nil {
		t.Fatal(err)
	}
	if _, _, err := provider.Get(context.Background(), settings.Global(), key.StableID()); err != nil {
		t.Fatal(err)
	}
	transport.mu.Lock()
	for cacheKey := range transport.values {
		transport.values[cacheKey] = []byte("not-json")
	}
	transport.mu.Unlock()
	record, ok, err := provider.Get(context.Background(), settings.Global(), key.StableID())
	if err != nil || !ok || string(record.Data) != "durable" {
		t.Fatalf("malformed fallback = %#v, %v, %v", record, ok, err)
	}
	if _, _, err := provider.Watch(context.Background(), 0); err == nil {
		t.Fatal("watch accepted empty buffer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	events, errs, err := provider.Watch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	transport.messages <- []byte("not-json")
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("nil watcher decode error")
		}
	case <-time.After(time.Second):
		t.Fatal("watcher decode error not delivered")
	}
	cancel()
	for range events {
	}
}
func (transport *fakeTransport) Subscribe(context.Context, string) (<-chan []byte, <-chan error) {
	return transport.messages, transport.subscribeErrors
}

func TestCacheDefinesStaleAndOutageBehavior(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	durable := memory.New()
	transport := newFakeTransport()
	provider := cache.New(durable, transport, cache.Config{
		Prefix: "settings:test", TTL: time.Minute,
		ReadPolicy: cache.BoundedStale, OutagePolicy: cache.Bypass,
	})
	key := settings.NewKey("ui", "theme", settings.StringCodec{})
	scope := settings.Tenant("acme")
	change := settings.Change{Actor: "operator", Reason: "test"}
	if _, err := settings.Set(ctx, provider, scope, key, "light", change); err != nil {
		t.Fatalf("cached set: %v", err)
	}
	if _, err := settings.Set(ctx, durable, scope, key, "dark", change); err != nil {
		t.Fatalf("direct durable set: %v", err)
	}

	stale, ok, err := provider.Get(ctx, scope, key.StableID())
	if err != nil || !ok || string(stale.Data) != "light" {
		t.Fatalf("bounded stale get = %#v, %v, %v", stale, ok, err)
	}
	transport.mu.Lock()
	transport.getErr = errors.New("valkey unavailable")
	transport.mu.Unlock()
	fresh, ok, err := provider.Get(ctx, scope, key.StableID())
	if err != nil || !ok || string(fresh.Data) != "dark" {
		t.Fatalf("outage bypass get = %#v, %v, %v", fresh, ok, err)
	}
}

func TestWatchIsBoundedCoalescingAndCancellable(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	provider := cache.New(memory.New(), transport, cache.Config{Prefix: "settings:test", TTL: time.Minute})
	ctx, cancel := context.WithCancel(context.Background())
	events, errs, err := provider.Watch(ctx, 1)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	change := settings.Change{Actor: "operator", Reason: "test"}
	key := settings.NewKey("ui", "theme", settings.StringCodec{})
	for index := 0; index < 10; index++ {
		if _, err := settings.Set(context.Background(), provider, settings.Global(), key, "dark", change); err != nil {
			t.Fatalf("set %d: %v", index, err)
		}
	}
	select {
	case <-events:
	case err := <-errs:
		t.Fatalf("watch error: %v", err)
	case <-time.After(time.Second):
		t.Fatal("watch did not deliver")
	}
	cancel()
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("watch did not close")
		}
	}
}

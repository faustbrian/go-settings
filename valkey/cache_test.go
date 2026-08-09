package valkey_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	lastKey         string
	lastTTL         time.Duration
	lastChannel     string
}

func TestCacheNeverRegressesWhenWriteCompletionIsReordered(t *testing.T) {
	t.Parallel()

	transport := &reorderingTransport{
		fakeTransport: newFakeTransport(), firstEntered: make(chan struct{}), releaseFirst: make(chan struct{}),
	}
	durable := memory.New()
	provider := cache.New(durable, transport, cache.Config{TTL: time.Minute})
	key := settings.NewKey("fleet", "generation", settings.IntCodec{})
	change := settings.Change{Actor: "operator", Reason: "concurrent rollout"}

	first := make(chan error, 1)
	go func() {
		_, err := settings.Set(context.Background(), provider, settings.Global(), key, int64(1), change)
		first <- err
	}()
	select {
	case <-transport.firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first cache write did not start")
	}
	if _, err := settings.Set(t.Context(), provider, settings.Global(), key, int64(2), change); err != nil {
		t.Fatal(err)
	}
	close(transport.releaseFirst)
	if err := <-first; err != nil {
		t.Fatal(err)
	}

	record, ok, err := provider.Get(t.Context(), settings.Global(), key.StableID())
	if err != nil || !ok || record.Version != 2 || string(record.Data) != "2" {
		t.Fatalf("cached record regressed = (%+v, %v, %v)", record, ok, err)
	}
}

type reorderingTransport struct {
	*fakeTransport
	firstEntered chan struct{}
	releaseFirst chan struct{}
}

func (transport *reorderingTransport) SetIfNewer(ctx context.Context, key string, value []byte, ttl time.Duration, version uint64) error {
	var record settings.Record
	if err := json.Unmarshal(value, &record); err != nil {
		return err
	}
	if record.Version == 1 {
		close(transport.firstEntered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-transport.releaseFirst:
		}
	}
	return transport.fakeTransport.SetIfNewer(ctx, key, value, ttl, version)
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
func (transport *fakeTransport) SetIfNewer(_ context.Context, key string, value []byte, ttl time.Duration, version uint64) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.lastKey = key
	transport.lastTTL = ttl
	if transport.setErr != nil {
		return transport.setErr
	}
	if current, ok := transport.values[key]; ok {
		var record settings.Record
		if json.Unmarshal(current, &record) == nil && record.Version >= version {
			return nil
		}
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
func (transport *fakeTransport) Publish(_ context.Context, channel string, value []byte) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.lastChannel = channel
	if transport.publishErr != nil {
		return transport.publishErr
	}
	select {
	case transport.messages <- append([]byte(nil), value...):
	default:
	}
	return nil
}

func TestCacheDefaultsAndExplicitConfigurationReachTransport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		config     cache.Config
		wantPrefix string
		wantTTL    time.Duration
	}{
		{name: "zero values", wantPrefix: "settings:value:", wantTTL: time.Minute},
		{name: "negative ttl", config: cache.Config{TTL: -time.Second}, wantPrefix: "settings:value:", wantTTL: time.Minute},
		{name: "explicit", config: cache.Config{Prefix: "fleet", TTL: time.Millisecond}, wantPrefix: "fleet:value:", wantTTL: time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := newFakeTransport()
			provider := cache.New(memory.New(), transport, test.config)
			key := settings.NewKey("config", "value", settings.StringCodec{})
			if _, err := settings.Set(t.Context(), provider, settings.Global(), key, "value", settings.Change{
				Actor: "operator", Reason: "verify transport configuration",
			}); err != nil {
				t.Fatal(err)
			}
			transport.mu.Lock()
			defer transport.mu.Unlock()
			if !strings.HasPrefix(transport.lastKey, test.wantPrefix) || transport.lastTTL != test.wantTTL {
				t.Fatalf("transport configuration = (%q, %s)", transport.lastKey, transport.lastTTL)
			}
		})
	}
}

func TestCacheBypassReturnsDurableDataWhenCacheFillFails(t *testing.T) {
	t.Parallel()

	durable := memory.New()
	key := settings.NewKey("cache", "fill", settings.StringCodec{})
	change := settings.Change{Actor: "operator", Reason: "verify outage bypass"}
	if _, err := settings.Set(t.Context(), durable, settings.Global(), key, "durable", change); err != nil {
		t.Fatal(err)
	}
	transport := newFakeTransport()
	transport.setErr = errors.New("cache unavailable")
	provider := cache.New(durable, transport, cache.Config{OutagePolicy: cache.Bypass})

	record, ok, err := provider.Get(t.Context(), settings.Global(), key.StableID())
	if err != nil || !ok || string(record.Data) != "durable" {
		t.Fatalf("single bypass = (%+v, %v, %v)", record, ok, err)
	}
	records, err := provider.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{key.StableID()})
	if err != nil || len(records) != 1 || string(records[0].Data) != "durable" {
		t.Fatalf("bulk bypass = (%+v, %v)", records, err)
	}
}

func TestWatchAcceptsExactBoundsAndUsesConfiguredChannel(t *testing.T) {
	t.Parallel()

	for _, buffer := range []int{1, 10_000} {
		transport := newFakeTransport()
		provider := cache.New(memory.New(), transport, cache.Config{Prefix: "fleet"})
		ctx, cancel := context.WithCancel(t.Context())
		events, errs, err := provider.Watch(ctx, buffer)
		if err != nil {
			t.Fatalf("buffer %d: %v", buffer, err)
		}
		transport.mu.Lock()
		channel := transport.lastChannel
		transport.mu.Unlock()
		if channel != "fleet:invalidate" {
			t.Fatalf("buffer %d channel = %q", buffer, channel)
		}
		cancel()
		for range events {
		}
		for range errs {
		}
	}
	for _, buffer := range []int{0, 10_001} {
		provider := cache.New(memory.New(), newFakeTransport(), cache.Config{})
		if _, _, err := provider.Watch(t.Context(), buffer); err == nil {
			t.Fatalf("invalid buffer %d accepted", buffer)
		}
	}
}

func TestWatchContinuesAfterMalformedInvalidationAndNeverForwardsNilErrors(t *testing.T) {
	t.Parallel()

	t.Run("malformed then valid", func(t *testing.T) {
		transport := newFakeTransport()
		provider := cache.New(memory.New(), transport, cache.Config{})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		events, errs, err := provider.Watch(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		want := cache.Event{ProtocolVersion: settings.InvalidationProtocolVersion, Scope: settings.Global(), Key: "fleet/key", Version: 7, State: settings.StateValue}
		encoded, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		transport.messages <- []byte("not-json")
		transport.messages <- encoded
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("nil decode error")
			}
		case <-time.After(time.Second):
			t.Fatal("decode error not delivered")
		}
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("event = %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("valid event after malformed message not delivered")
		}
	})

	t.Run("nil transport error", func(t *testing.T) {
		transport := newFakeTransport()
		provider := cache.New(memory.New(), transport, cache.Config{})
		events, errs, err := provider.Watch(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		transport.subscribeErrors <- nil
		if _, ok := <-events; ok {
			t.Fatal("event channel remained open")
		}
		if err, ok := <-errs; ok {
			t.Fatalf("nil transport error was forwarded: %v", err)
		}
	})
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
	transport.setErr = errors.New("set unavailable")
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
func (transport *fakeTransport) Subscribe(_ context.Context, channel string) (<-chan []byte, <-chan error) {
	transport.mu.Lock()
	transport.lastChannel = channel
	transport.mu.Unlock()
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

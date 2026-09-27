package valkey_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	settings "github.com/faustbrian/go-settings/v2"
	"github.com/faustbrian/go-settings/v2/memory"
	"github.com/faustbrian/go-settings/v2/settingstest"
	cache "github.com/faustbrian/go-settings/v2/valkey"
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
	provider := cache.New(durable, transport, cache.Config{Prefix: "reordering", TTL: time.Minute})
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
			Prefix: "conformance", ReadPolicy: cache.Strong, OutagePolicy: cache.FailClosed,
		})
	})
}

func TestCacheRejectsSharedDefaultNamespaceBeforeCrossDeploymentRead(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	firstDurable := memory.New()
	secondDurable := memory.New()
	key := settings.NewKey("shared", "credential", settings.StringCodec{})
	change := settings.Change{Actor: "operator", Reason: "isolate deployments"}
	if _, err := settings.Set(t.Context(), firstDurable, settings.Global(), key, "first", change); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Set(t.Context(), secondDurable, settings.Global(), key, "second", change); err != nil {
		t.Fatal(err)
	}
	first := cache.New(firstDurable, transport, cache.Config{})
	second := cache.New(secondDurable, transport, cache.Config{})
	if _, _, err := first.Get(t.Context(), settings.Global(), key.StableID()); err == nil || !strings.Contains(err.Error(), "namespace is required") {
		t.Fatalf("first cache without namespace error = %v", err)
	}
	if _, _, err := second.Get(t.Context(), settings.Global(), key.StableID()); err == nil || !strings.Contains(err.Error(), "namespace is required") {
		t.Fatalf("second cache without namespace error = %v", err)
	}
}

func TestCacheRejectsEveryUnsafeNamespaceBeforeDurableUse(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{
		" ", strings.Repeat("x", 256), "\x00app", "app\x00prod", "app\tprod", "app\nprod", "app\rprod", "app\x7fprod",
	} {
		prefix := prefix
		t.Run(fmt.Sprintf("%q", prefix), func(t *testing.T) {
			durable := memory.New()
			provider := cache.New(durable, newFakeTransport(), cache.Config{Prefix: prefix})
			key := settings.NewKey("namespace", "credential", settings.StringCodec{})
			mutation, err := settings.PrepareSet(settings.Global(), key, "secret", nil,
				settings.Change{Actor: "operator", Reason: "reject unsafe namespace"})
			if err != nil {
				t.Fatal(err)
			}

			if _, _, err := provider.Get(t.Context(), settings.Global(), key.StableID()); err == nil {
				t.Fatal("get accepted unsafe namespace")
			}
			if _, err := provider.BulkGet(t.Context(), []settings.Scope{settings.Global()}, []string{key.StableID()}); err == nil {
				t.Fatal("bulk get accepted unsafe namespace")
			}
			if _, err := provider.Apply(t.Context(), mutation); err == nil {
				t.Fatal("apply accepted unsafe namespace")
			}
			if _, err := provider.BulkApply(t.Context(), []settings.Mutation{mutation}); err == nil {
				t.Fatal("bulk apply accepted unsafe namespace")
			}
			if _, err := provider.History(t.Context(), settings.HistoryQuery{Scope: settings.Global(), Limit: 1}); err == nil {
				t.Fatal("history accepted unsafe namespace")
			}
			if _, _, err := provider.Watch(t.Context(), 1); err == nil {
				t.Fatal("watch accepted unsafe namespace")
			}
			if _, present, err := durable.Get(t.Context(), settings.Global(), key.StableID()); err != nil || present {
				t.Fatalf("unsafe namespace reached durable provider: present=%v err=%v", present, err)
			}
		})
	}
}

func TestCacheAcceptsExactNamespaceLimitAndRejectsInvalidWatchBeforeSubscription(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	invalid := cache.New(memory.New(), transport, cache.Config{})
	if events, errs, err := invalid.Watch(t.Context(), 1); err == nil || events != nil || errs != nil {
		t.Fatalf("invalid namespace Watch = (%v, %v, %v)", events, errs, err)
	}
	transport.mu.Lock()
	channel := transport.lastChannel
	transport.mu.Unlock()
	if channel != "" {
		t.Fatalf("invalid namespace subscribed to %q", channel)
	}

	prefix := strings.Repeat("x", 255)
	provider := cache.New(memory.New(), transport, cache.Config{Prefix: prefix})
	key := settings.NewKey("boundary", "namespace", settings.StringCodec{})
	if _, err := settings.Set(t.Context(), provider, settings.Global(), key, "value", settings.Change{
		Actor: "operator", Reason: "exact namespace limit",
	}); err != nil {
		t.Fatalf("255-byte namespace write: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	provider = cache.New(memory.New(), newFakeTransport(), cache.Config{Prefix: prefix})
	events, errs, err := provider.Watch(ctx, 1)
	if err != nil {
		t.Fatalf("255-byte namespace watch: %v", err)
	}
	cancel()
	select {
	case _, open := <-events:
		if open {
			t.Fatal("cancelled watcher delivered an event")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled watcher did not close events")
	}
	select {
	case _, open := <-errs:
		if open {
			t.Fatal("cancelled watcher delivered an error")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled watcher did not close errors")
	}
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

func TestCacheTTLDefaultsAndExplicitConfigurationReachTransport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		config     cache.Config
		wantPrefix string
		wantTTL    time.Duration
	}{
		{name: "zero ttl", config: cache.Config{Prefix: "defaults"}, wantPrefix: "defaults:value:", wantTTL: time.Minute},
		{name: "negative ttl", config: cache.Config{Prefix: "defaults", TTL: -time.Second}, wantPrefix: "defaults:value:", wantTTL: time.Minute},
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
	provider := cache.New(durable, transport, cache.Config{Prefix: "bypass", OutagePolicy: cache.Bypass})

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
		assertWatcherClosed(t, events, errs)
	}
	for _, buffer := range []int{0, 10_001} {
		provider := cache.New(memory.New(), newFakeTransport(), cache.Config{Prefix: "bounds"})
		if _, _, err := provider.Watch(t.Context(), buffer); err == nil {
			t.Fatalf("invalid buffer %d accepted", buffer)
		}
	}
}

func TestWatchContinuesAfterMalformedInvalidationAndNeverForwardsNilErrors(t *testing.T) {
	t.Parallel()

	t.Run("malformed then valid", func(t *testing.T) {
		transport := newFakeTransport()
		provider := cache.New(memory.New(), transport, cache.Config{Prefix: "malformed"})
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

	t.Run("hostile numeric token is redacted", func(t *testing.T) {
		transport := newFakeTransport()
		provider := cache.New(memory.New(), transport, cache.Config{Prefix: "redacted"})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		_, errs, err := provider.Watch(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		const hostileToken = "1e123456789"
		transport.messages <- []byte(`{"version":` + hostileToken + `}`)
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("nil decode error")
			}
			var cacheErr *cache.CacheError
			if !errors.As(err, &cacheErr) || cacheErr.Operation != "decode invalidation" || cacheErr.Committed {
				t.Fatalf("decode error classification = %#v", err)
			}
			if strings.Contains(err.Error(), hostileToken) {
				t.Fatalf("decode error exposed hostile token: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("decode error not delivered")
		}
	})

	t.Run("nil transport error", func(t *testing.T) {
		transport := newFakeTransport()
		provider := cache.New(memory.New(), transport, cache.Config{Prefix: "nil-error"})
		events, errs, err := provider.Watch(t.Context(), 1)
		if err != nil {
			t.Fatal(err)
		}
		transport.subscribeErrors <- nil
		assertClosedWatchChannel(t, events)
		assertClosedWatchChannel(t, errs)
	})
}

func TestWatchAcceptsExactInvalidationByteLimit(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	provider := cache.New(memory.New(), transport, cache.Config{Prefix: "exact-bound"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, errs, err := provider.Watch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := cache.Event{ProtocolVersion: settings.InvalidationProtocolVersion, Scope: settings.Global(), Key: "fleet/key", Version: 7, State: settings.StateValue}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, []byte(strings.Repeat(" ", (4<<10)-len(encoded)))...)
	transport.messages <- encoded
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("exact-limit event = %+v, want %+v", got, want)
		}
	case err := <-errs:
		t.Fatalf("4096-byte valid invalidation rejected: %v", err)
	case <-time.After(time.Second):
		t.Fatal("4096-byte valid invalidation not delivered")
	}
}

func TestWatchRejectsOversizedInvalidationBeforeDecoding(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	provider := cache.New(memory.New(), transport, cache.Config{Prefix: "bounded"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	events, errs, err := provider.Watch(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	transport.messages <- make([]byte, 4<<10+1)
	select {
	case err := <-errs:
		if err == nil || !strings.Contains(err.Error(), "exceeds 4 KiB") {
			t.Fatalf("oversized invalidation error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("oversized invalidation was not rejected")
	}
	select {
	case event := <-events:
		t.Fatalf("oversized invalidation forwarded as %+v", event)
	default:
	}
}

func TestCacheStrongBulkHistoryAndFailureContracts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	durable := memory.New()
	transport := newFakeTransport()
	provider := cache.New(durable, transport, cache.Config{
		Prefix: "strong", ReadPolicy: cache.Strong, OutagePolicy: cache.FailClosed,
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
	assertWatcherClosed(t, events, errs)
}

func assertWatcherClosed(t testing.TB, events <-chan cache.Event, errs <-chan error) {
	t.Helper()
	if events == nil || errs == nil {
		t.Fatal("successful Watch returned nil channels")
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for events != nil || errs != nil {
		select {
		case _, open := <-events:
			if !open {
				events = nil
			}
		case _, open := <-errs:
			if !open {
				errs = nil
			}
		case <-timer.C:
			t.Fatal("watcher did not close channels after cancellation")
		}
	}
}

func assertClosedWatchChannel[T any](t testing.TB, channel <-chan T) {
	t.Helper()
	select {
	case _, open := <-channel:
		if open {
			t.Fatal("watch channel delivered a value instead of closing")
		}
	case <-time.After(time.Second):
		t.Fatal("watch channel did not close")
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

func TestWatchCoalescesToNewestEvent(t *testing.T) {
	t.Parallel()

	transport := newFakeTransport()
	provider := cache.New(memory.New(), transport, cache.Config{Prefix: "settings:test", TTL: time.Minute})
	events, errs, err := provider.Watch(t.Context(), 1)
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
	close(transport.messages)
	select {
	case watchErr, open := <-errs:
		if open {
			t.Fatalf("watch error = %v", watchErr)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not stop after transport closed")
	}
	select {
	case event := <-events:
		if event.Version != 10 {
			t.Fatalf("coalesced event version = %d, want newest version 10", event.Version)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not deliver")
	}
	assertClosedWatchChannel(t, events)
}

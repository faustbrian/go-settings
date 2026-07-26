// Package valkey provides an optional cache and invalidation layer in front of
// a durable settings provider. It is never durable by default.
package valkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	settings "github.com/faustbrian/golib/pkg/settings"
)

// Transport is the small Valkey contract needed by Cache.
type Transport interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
	Publish(context.Context, string, []byte) error
	Subscribe(context.Context, string) (<-chan []byte, <-chan error)
}

// ReadPolicy defines whether reads may be served from an invalidation-driven
// cache or must always consult durable storage.
type ReadPolicy uint8

const (
	Strong ReadPolicy = iota + 1
	BoundedStale
)

// OutagePolicy defines cache outage behavior.
type OutagePolicy uint8

const (
	Bypass OutagePolicy = iota
	FailClosed
)

// Config makes consistency and outage behavior explicit.
type Config struct {
	Prefix       string
	TTL          time.Duration
	ReadPolicy   ReadPolicy
	OutagePolicy OutagePolicy
}

// Event is an at-most-once invalidation notification. Watch may coalesce
// events when its bounded consumer channel is full.
type Event struct {
	Scope   settings.Scope `json:"scope"`
	Key     string         `json:"key"`
	Version uint64         `json:"version"`
}

// CacheError reports a cache-side failure and whether the durable mutation
// already committed.
type CacheError struct {
	Operation string
	Committed bool
	Err       error
}

func (err *CacheError) Error() string {
	return fmt.Sprintf("settings valkey %s: %v", err.Operation, err.Err)
}
func (err *CacheError) Unwrap() error { return err.Err }

// Cache wraps a durable provider with bounded-stale reads and invalidation.
type Cache struct {
	durable   settings.Provider
	transport Transport
	config    Config
	channel   string
}

// New constructs a cache. Empty prefixes and non-positive TTLs are replaced
// with deployment-safe defaults; BoundedStale is the default read policy.
func New(durable settings.Provider, transport Transport, config Config) *Cache {
	if config.Prefix == "" {
		config.Prefix = "settings"
	}
	if config.TTL <= 0 {
		config.TTL = time.Minute
	}
	if config.ReadPolicy == 0 {
		config.ReadPolicy = BoundedStale
	}
	return &Cache{
		durable: durable, transport: transport, config: config,
		channel: config.Prefix + ":invalidate",
	}
}

func (cache *Cache) Capabilities() settings.Capabilities {
	capabilities := cache.durable.Capabilities()
	capabilities.Subscriptions = true
	return capabilities
}

func (cache *Cache) Get(ctx context.Context, scope settings.Scope, key string) (settings.Record, bool, error) {
	if cache.config.ReadPolicy == BoundedStale {
		data, ok, err := cache.transport.Get(ctx, cache.key(scope, key))
		if err == nil && ok {
			record, decodeErr := decodeRecord(data, scope, key)
			if decodeErr == nil {
				return record, record.State != settings.StateMissing, nil
			}
			_ = cache.transport.Delete(ctx, cache.key(scope, key))
		} else if err != nil && cache.config.OutagePolicy == FailClosed {
			return settings.Record{}, false, &CacheError{Operation: "get", Err: err}
		}
	}
	record, ok, err := cache.durable.Get(ctx, scope, key)
	if err != nil {
		return settings.Record{}, false, err
	}
	if ok {
		if cacheErr := cache.store(ctx, record); cacheErr != nil && cache.config.OutagePolicy == FailClosed {
			return settings.Record{}, false, cacheErr
		}
	}
	return record, ok, nil
}

// BulkGet always uses the durable provider's snapshot-capable bulk operation,
// then refreshes cache entries. This avoids mixing versions in snapshots.
func (cache *Cache) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	records, err := cache.durable.BulkGet(ctx, scopes, keys)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if cacheErr := cache.store(ctx, record); cacheErr != nil && cache.config.OutagePolicy == FailClosed {
			return nil, cacheErr
		}
	}
	return records, nil
}

func (cache *Cache) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	record, err := cache.durable.Apply(ctx, mutation)
	if err != nil {
		return settings.Record{}, err
	}
	if err := cache.afterWrite(ctx, record); err != nil {
		return record, err
	}
	return record, nil
}

func (cache *Cache) BulkApply(ctx context.Context, mutations []settings.Mutation) ([]settings.Record, error) {
	records, err := cache.durable.BulkApply(ctx, mutations)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if err := cache.afterWrite(ctx, record); err != nil {
			return records, err
		}
	}
	return records, nil
}

func (cache *Cache) afterWrite(ctx context.Context, record settings.Record) error {
	var err error
	if record.State == settings.StateMissing {
		err = cache.transport.Delete(ctx, cache.key(record.Scope, record.Key))
	} else {
		err = cache.store(ctx, record)
	}
	if err != nil && cache.config.OutagePolicy == FailClosed {
		return &CacheError{Operation: "read-after-write", Committed: true, Err: err}
	}
	event, _ := json.Marshal(Event{Scope: record.Scope, Key: record.Key, Version: record.Version})
	if publishErr := cache.transport.Publish(ctx, cache.channel, event); publishErr != nil &&
		cache.config.OutagePolicy == FailClosed {
		return &CacheError{Operation: "publish invalidation", Committed: true, Err: publishErr}
	}
	return nil
}

func (cache *Cache) store(ctx context.Context, record settings.Record) error {
	data, _ := json.Marshal(record)
	if err := cache.transport.Set(ctx, cache.key(record.Scope, record.Key), data, cache.config.TTL); err != nil {
		return &CacheError{Operation: "set", Err: err}
	}
	return nil
}

func decodeRecord(data []byte, scope settings.Scope, key string) (settings.Record, error) {
	if len(data) > 2<<20 {
		return settings.Record{}, errors.New("cached record exceeds 2 MiB")
	}
	var record settings.Record
	if err := json.Unmarshal(data, &record); err != nil {
		return settings.Record{}, fmt.Errorf("decode cached record: %w", err)
	}
	if record.Scope != scope || record.Key != key || record.Version == 0 ||
		(record.State != settings.StateValue && record.State != settings.StateCleared) ||
		len(record.Data) > 1<<20 {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	return record, nil
}

func (cache *Cache) key(scope settings.Scope, key string) string {
	sum := sha256.Sum256([]byte(scope.String() + "\x00" + key))
	return cache.config.Prefix + ":value:" + hex.EncodeToString(sum[:])
}

func (cache *Cache) History(ctx context.Context, query settings.HistoryQuery) ([]settings.ChangeRecord, error) {
	return cache.durable.History(ctx, query)
}

// Watch subscribes to bounded, cancellable, at-most-once invalidations. When
// the buffer is full, the oldest queued event is replaced by the newest.
func (cache *Cache) Watch(ctx context.Context, buffer int) (<-chan Event, <-chan error, error) {
	if buffer < 1 || buffer > 10_000 {
		return nil, nil, fmt.Errorf("settings valkey: watcher buffer must be between 1 and 10000")
	}
	messages, transportErrors := cache.transport.Subscribe(ctx, cache.channel)
	events := make(chan Event, buffer)
	errorsOut := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errorsOut)
		for {
			select {
			case <-ctx.Done():
				return
			case err, ok := <-transportErrors:
				if ok && err != nil {
					select {
					case errorsOut <- err:
					default:
					}
				}
				return
			case message, ok := <-messages:
				if !ok {
					return
				}
				var event Event
				if err := json.Unmarshal(message, &event); err != nil {
					select {
					case errorsOut <- fmt.Errorf("settings valkey decode invalidation: %w", err):
					default:
					}
					continue
				}
				select {
				case events <- event:
				default:
					select {
					case <-events:
					default:
					}
					select {
					case events <- event:
					default:
					}
				}
			}
		}
	}()
	return events, errorsOut, nil
}

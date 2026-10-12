// Package valkey provides an optional cache and invalidation layer in front of
// a durable settings provider. It is never durable by default. Use New with a
// borrowed durable source and Transport; NewNativeTransport introduces valkey-go
// dependencies. Callers own client close, credentials, transport cancellation,
// tenant admission and value encryption. Pub/sub hints do not themselves evict
// this Cache's entries or provide delivery/cluster freshness guarantees.
package valkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	settings "github.com/faustbrian/go-settings/v3"
)

// Transport is the small Valkey contract needed by Cache.
type Transport interface {
	// Get returns cached bytes and presence; implementations must support concurrent
	// context-aware use.
	Get(context.Context, string) ([]byte, bool, error)
	// SetIfNewer atomically stores bytes with TTL only when the supplied version is newer than
	// retained state.
	SetIfNewer(context.Context, string, []byte, time.Duration, uint64) error
	// Delete removes one cached key.
	Delete(context.Context, string) error
	// Publish sends a lossy invalidation, not a durable delivery acknowledgement.
	Publish(context.Context, string, []byte) error
	// Subscribe opens a context-owned subscription; implementations close both channels when it ends.
	Subscribe(context.Context, string) (<-chan []byte, <-chan error)
}

// ReadPolicy defines whether reads may be served from a TTL-based
// cache or must always consult durable storage.
type ReadPolicy uint8

const (
	// Strong consults durable storage on every read.
	Strong ReadPolicy = iota + 1
	// BoundedStale allows a validated cached record until transport TTL expiry.
	BoundedStale
)

// OutagePolicy defines cache outage behavior.
type OutagePolicy uint8

const (
	// Bypass suppresses cache failures and uses durable storage.
	Bypass OutagePolicy = iota
	// FailClosed returns cache failures, including failures after a durable commit.
	FailClosed
)

// Config makes consistency and outage behavior explicit.
type Config struct {
	// Prefix is a deployment-unique namespace, 1–255 bytes without controls; naming is not
	// access control.
	Prefix string
	// TTL defaults to one minute when nonpositive; it bounds cache entry residence, not a
	// cluster freshness SLA.
	TTL time.Duration
	// ReadPolicy defaults to BoundedStale when zero; other non-BoundedStale values consult
	// durable storage.
	ReadPolicy ReadPolicy
	// OutagePolicy defaults to Bypass; only FailClosed propagates cache-side failures.
	OutagePolicy OutagePolicy
}

// Event is the shared versioned invalidation hint.
type Event = settings.Invalidation

var errInvalidInvalidation = errors.New("invalidation payload is invalid")

// CacheError reports a cache-side failure and whether the durable mutation
// already committed.
type CacheError struct {
	// Operation names the cache failure boundary.
	Operation string
	// Committed is true when a durable write succeeded before cache/publish failed.
	Committed bool
	// Err is the wrapped transport cause; transports must not expose secrets in errors.
	Err error
}

// Error renders operation and wrapped cause, relying on transport errors to be secret-safe.
func (err *CacheError) Error() string {
	return fmt.Sprintf("settings valkey %s: %v", err.Operation, err.Err)
}

// Unwrap returns the underlying transport failure.
func (err *CacheError) Unwrap() error { return err.Err }

// Cache wraps a durable provider with TTL-based reads and invalidation publishing.
// It has no owned Close lifecycle. Concurrent use depends on its borrowed
// durable provider and Transport. Zero Cache is not usable; Watch contexts own
// their goroutines. Cache values may contain secret bytes even when audit
// redaction is enabled, so deployment access control/encryption remain external.
type Cache struct {
	durable   settings.Provider
	transport Transport
	config    Config
	channel   string
	configErr error
}

// New constructs a cache. Prefix must identify one deployment; an invalid
// prefix makes IO operations fail closed. TTL <= 0 becomes one minute and zero
// ReadPolicy becomes BoundedStale; only the prefix is validated. Non-nil durable
// and transport collaborators are required but not checked. Other enum values
// behave according to their branch comparison rather than being rejected.
func New(durable settings.Provider, transport Transport, config Config) *Cache {
	configErr := validateConfig(config)
	if config.TTL <= 0 {
		config.TTL = time.Minute
	}
	if config.ReadPolicy == 0 {
		config.ReadPolicy = BoundedStale
	}
	return &Cache{
		durable: durable, transport: transport, config: config,
		channel: config.Prefix + ":invalidate", configErr: configErr,
	}
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.Prefix) == "" {
		return errors.New("settings valkey: deployment-unique namespace is required")
	}
	if len(config.Prefix) > 255 || strings.IndexFunc(config.Prefix, unicode.IsControl) >= 0 {
		return errors.New("settings valkey: namespace must be at most 255 bytes without control characters")
	}
	return nil
}

// Capabilities delegates durable guarantees and adds Subscriptions; configuration
// errors are reported by IO methods rather than this metadata call.
func (cache *Cache) Capabilities() settings.Capabilities {
	capabilities := cache.durable.Capabilities()
	capabilities.Subscriptions = true
	return capabilities
}

// Get uses cached records only under BoundedStale, rejecting malformed payloads and falling
// back to durable storage. FailClosed propagates transport/fill errors; missing durable reads
// are not newly cached.
func (cache *Cache) Get(ctx context.Context, scope settings.Scope, key string) (settings.Record, bool, error) {
	if cache.configErr != nil {
		return settings.Record{}, false, cache.configErr
	}
	if cache.config.ReadPolicy == BoundedStale {
		data, ok, err := cache.transport.Get(ctx, cache.key(scope, key))
		if err == nil {
			if ok {
				record, decodeErr := decodeRecord(data, scope, key)
				if decodeErr == nil {
					return record, record.State != settings.StateMissing, nil
				}
				_ = cache.transport.Delete(ctx, cache.key(scope, key))
			}
		} else if cache.config.OutagePolicy == FailClosed {
			return settings.Record{}, false, &CacheError{Operation: "get", Err: err}
		}
	}
	record, ok, err := cache.durable.Get(ctx, scope, key)
	if err != nil {
		return settings.Record{}, false, err
	}
	if ok {
		if cacheErr := cache.store(ctx, record); cacheErr != nil {
			if cache.config.OutagePolicy == FailClosed {
				return settings.Record{}, false, cacheErr
			}
		}
	}
	return record, ok, nil
}

// BulkGet always uses the durable provider's snapshot-capable bulk operation,
// then refreshes cache entries. This avoids mixing versions in snapshots.
func (cache *Cache) BulkGet(ctx context.Context, scopes []settings.Scope, keys []string) ([]settings.Record, error) {
	if cache.configErr != nil {
		return nil, cache.configErr
	}
	records, err := cache.durable.BulkGet(ctx, scopes, keys)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if cacheErr := cache.store(ctx, record); cacheErr != nil {
			if cache.config.OutagePolicy == FailClosed {
				return nil, cacheErr
			}
		}
	}
	return records, nil
}

// Apply writes durable state first, then caches/publishes. A committed CacheError accompanies
// the acknowledged record under FailClosed; do not blindly retry.
func (cache *Cache) Apply(ctx context.Context, mutation settings.Mutation) (settings.Record, error) {
	if cache.configErr != nil {
		return settings.Record{}, cache.configErr
	}
	record, err := cache.durable.Apply(ctx, mutation)
	if err != nil {
		return settings.Record{}, err
	}
	if err := cache.afterWrite(ctx, record); err != nil {
		return record, err
	}
	return record, nil
}

// BulkApply writes the durable batch first, then sequentially fans out its records; a later
// committed cache error returns the full durable result, with partial fanout.
func (cache *Cache) BulkApply(ctx context.Context, mutations []settings.Mutation) ([]settings.Record, error) {
	if cache.configErr != nil {
		return nil, cache.configErr
	}
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
	err := cache.store(ctx, record)
	if err != nil && cache.config.OutagePolicy == FailClosed {
		return &CacheError{Operation: "read-after-write", Committed: true, Err: err}
	}
	event, _ := json.Marshal(Event{
		ProtocolVersion: settings.InvalidationProtocolVersion,
		Scope:           record.Scope, Key: record.Key, Version: record.Version, State: record.State,
	})
	if publishErr := cache.transport.Publish(ctx, cache.channel, event); publishErr != nil &&
		cache.config.OutagePolicy == FailClosed {
		return &CacheError{Operation: "publish invalidation", Committed: true, Err: publishErr}
	}
	return nil
}

func (cache *Cache) store(ctx context.Context, record settings.Record) error {
	data, _ := json.Marshal(record)
	if err := cache.transport.SetIfNewer(ctx, cache.key(record.Scope, record.Key), data, cache.config.TTL, record.Version); err != nil {
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
	if record.Scope != scope {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	if record.Key != key {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	if record.Version == 0 {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	if record.State != settings.StateMissing && record.State != settings.StateValue && record.State != settings.StateCleared {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	if len(record.Data) > 1<<20 {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	if record.State != settings.StateValue && len(record.Data) != 0 {
		return settings.Record{}, errors.New("cached record contract mismatch")
	}
	return record, nil
}

func (cache *Cache) key(scope settings.Scope, key string) string {
	sum := sha256.Sum256([]byte(scope.String() + "\x00" + key))
	return cache.config.Prefix + ":value:" + hex.EncodeToString(sum[:])
}

// History delegates directly to durable history after configuration admission.
func (cache *Cache) History(ctx context.Context, query settings.HistoryQuery) ([]settings.ChangeRecord, error) {
	if cache.configErr != nil {
		return nil, cache.configErr
	}
	return cache.durable.History(ctx, query)
}

// Watch subscribes to bounded, cancellable, at-most-once invalidations. When
// the buffer is full, the oldest queued event is replaced by the newest. Buffer
// must be 1–10000; messages are bounded to 4 KiB before JSON decode, but semantic
// hint admission is left to consumers. Decode errors are reported nonblocking;
// transport error-channel closure or a nil error ends this watcher. Cancel the
// nonnil context to close both returned channels; no durable replay is supplied.
func (cache *Cache) Watch(ctx context.Context, buffer int) (<-chan Event, <-chan error, error) {
	if cache.configErr != nil {
		return nil, nil, cache.configErr
	}
	if buffer < 1 {
		return nil, nil, fmt.Errorf("settings valkey: watcher buffer must be between 1 and 10000")
	}
	if buffer > 10_000 {
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
				if ok {
					if err == nil {
						return
					}
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
				var decodeErr error
				if len(message) > 4<<10 {
					decodeErr = errors.New("invalidation message exceeds 4 KiB")
				} else {
					if err := json.Unmarshal(message, &event); err != nil {
						decodeErr = errInvalidInvalidation
					}
				}
				if decodeErr != nil {
					select {
					case errorsOut <- &CacheError{Operation: "decode invalidation", Err: decodeErr}:
					default:
					}
				} else {
					for len(events) == cap(events) {
						select {
						case <-events:
						default:
						}
					}
					events <- event
				}
			}
		}
	}()
	return events, errorsOut, nil
}

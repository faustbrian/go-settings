package settings

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrSnapshotUnavailable reports absent usable last-known-good state.
	ErrSnapshotUnavailable = errors.New("settings: snapshot unavailable")
	// ErrSnapshotStale reports policy rejection of bounded stale state.
	ErrSnapshotStale = errors.New("settings: snapshot stale")
	// ErrSnapshotExpired reports age beyond the policy ceiling.
	ErrSnapshotExpired = errors.New("settings: snapshot expired")
	// ErrDefaultUnavailable reports a selected default policy without a registered default.
	ErrDefaultUnavailable = errors.New("settings: default unavailable")
	// ErrRuntimeStarted reports repeated or concurrent startup.
	ErrRuntimeStarted = errors.New("settings: runtime already started")
	// ErrRuntimeClosed reports startup after permanent Shutdown.
	ErrRuntimeClosed = errors.New("settings: runtime closed")
	// ErrNonMonotonicSnapshot reports a lost/lower retained coordinate or failed
	// acknowledged-write fence.
	ErrNonMonotonicSnapshot = errors.New("settings: non-monotonic snapshot")
)

// CommittedWriteError reports that durable state advanced even though
// reconciliation or cache fanout did not complete.
type CommittedWriteError struct {
	// Operation identifies the failed reconciliation/fanout boundary.
	Operation string
	// Committed reports that durable state advanced; do not assume an error means no write.
	Committed bool
	// Err is the wrapped cause, which caller-owned components must keep secret-safe.
	Err error
}

// Error renders operation and wrapped cause; external components are responsible for safe
// error text.
func (err *CommittedWriteError) Error() string {
	return fmt.Sprintf("settings %s after durable commit: %v", err.Operation, err.Err)
}

// Unwrap returns the underlying failure.
func (err *CommittedWriteError) Unwrap() error { return err.Err }

// AvailabilityAction defines the only allowed response to unavailable or
// non-fresh state.
type AvailabilityAction uint8

const (
	// ServeLastKnownGood permits bounded stale state only, not unavailable or expired state.
	ServeLastKnownGood AvailabilityAction = iota + 1
	// UseDefault requires an available registered default.
	UseDefault
	// FailClosed returns the applicable availability error rather than a fallback.
	FailClosed
)

// ClassPolicy bounds how long one setting class may use a last-known-good
// snapshot and explicitly selects each degradation outcome.
type ClassPolicy struct {
	// FreshFor is the inclusive fresh age threshold; zero is permitted.
	FreshFor time.Duration
	// MaxStaleness is the inclusive last-known-good ceiling, from FreshFor through 24 hours.
	MaxStaleness time.Duration
	// OnUnavailable selects default or fail-closed when no snapshot exists.
	OnUnavailable AvailabilityAction
	// OnStale selects behavior after FreshFor through MaxStaleness.
	OnStale AvailabilityAction
	// OnExpired selects default or fail-closed beyond MaxStaleness.
	OnExpired AvailabilityAction
}

// RuntimeClock supplies freshness time without hiding a process-global clock.
type RuntimeClock interface {
	// Now returns freshness time and must be safe for concurrent calls.
	Now() time.Time
}

type systemRuntimeClock struct{}

func (systemRuntimeClock) Now() time.Time { return time.Now() }

// RefreshExecutor composes retry, circuit-breaker, bulkhead, adaptive, and
// shared-budget policies around one complete refresh attempt. Operations must
// run synchronously and serially, never after Execute returns. Implementations
// must honor context; the runtime neither contains panics nor adds retries.
type RefreshExecutor interface {
	// Execute runs refresh attempts synchronously and serially within the supplied context; do
	// not retain or invoke operation after return.
	Execute(context.Context, func(context.Context) error) error
}

// RefreshExecutorFunc adapts a policy-composed function to RefreshExecutor.
type RefreshExecutorFunc func(context.Context, func(context.Context) error) error

// Execute invokes the borrowed function synchronously without panic containment; a nil
// function panics.
func (executor RefreshExecutorFunc) Execute(ctx context.Context, operation func(context.Context) error) error {
	return executor(ctx, operation)
}

type directRefreshExecutor struct{}

// Execute invokes the borrowed function synchronously without panic containment; a nil
// function panics.
func (directRefreshExecutor) Execute(ctx context.Context, operation func(context.Context) error) error {
	return operation(ctx)
}

// SnapshotStore persists an encrypted, caller-owned last-known-good document
// for cold-start recovery. Runtime passes plaintext wire bytes: implementations
// must encrypt/authenticate them on Save and decrypt/authenticate on Load.
// IO must honor context and remain safe for concurrent calls; Runtime does not
// close storage or erase serialized buffers.
type SnapshotStore interface {
	// Load returns authenticated/decrypted document bytes and presence; implementations own
	// storage authentication and decryption before Runtime validation.
	Load(context.Context) ([]byte, bool, error)
	// Save persists the document within context; caller owns encryption and must not retain
	// mutable caller buffers.
	Save(context.Context, []byte) error
}

// RuntimeConfig defines the durable source, immutable coordinate set, explicit
// class policies, and bound for one logical refresh.
type RuntimeConfig struct {
	// Provider is a borrowed source requiring coherent snapshots and cooperative context-aware IO.
	Provider Provider
	// Chain is the immutable precedence coordinate set.
	Chain ResolutionChain
	// Definitions is copied as a slice; implementations, codecs and callbacks remain borrowed.
	Definitions []Definition
	// Policies is copied and must cover every registered class with explicit outcomes.
	Policies map[SettingClass]ClassPolicy
	// Provenance labels captured state; it does not authenticate the provider.
	Provenance Provenance
	// RefreshTimeout bounds one logical refresh context to (0, 5 minutes]; custom work must honor it.
	RefreshTimeout time.Duration
	// Clock is a borrowed concurrency-safe clock; nil selects the system clock.
	Clock RuntimeClock
	// Executor is borrowed refresh policy execution; nil runs one direct attempt.
	Executor RefreshExecutor
	// RefreshInterval is zero to disable periodic refresh, otherwise 10 milliseconds–24 hours.
	RefreshInterval time.Duration
	// MaxJitter caps additive jitter to at most one hour; positive requires Jitter.
	MaxJitter time.Duration
	// Jitter is borrowed synchronous code; returns are clamped to [0, MaxJitter], without panic
	// containment.
	Jitter func(time.Duration) time.Duration
	// Invalidations is an optional borrowed hint source; no transport is opened by construction.
	Invalidations InvalidationSource
	// WatchBuffer is the subscription capacity (1–10000) when Invalidations is set.
	WatchBuffer int
	// ReconnectDelay is the reconnect wait (10 milliseconds–1 minute) when Invalidations is set.
	ReconnectDelay time.Duration
	// InvalidationDebounce is the coalesced-refresh delay (10 milliseconds–1 minute) when
	// Invalidations is set.
	InvalidationDebounce time.Duration
	// SnapshotStore is borrowed cache persistence; caller owns authenticated encryption and IO
	// lifetime.
	SnapshotStore SnapshotStore
}

type runtimeState struct{ snapshot Snapshot }

type refreshFlight struct {
	done chan struct{}
	err  error
}

type runtimeDefinitionContract struct {
	definition  Definition
	defaultData []byte
	hasDefault  bool
}

// Runtime owns one process-local last-known-good snapshot. Cluster convergence
// is driven by durable refreshes rather than shared mutable process state.
// Use NewRuntime; zero Runtime is not usable. Reads and refresh/write/lifecycle
// methods coordinate internal state, but custom collaborators must support
// concurrent use and honor context. Shutdown drains owned background work; it
// does not close borrowed resources or join external Apply/Refresh calls, which
// remain callable after shutdown. No user callback is panic-contained.
type Runtime struct {
	provider        Provider
	chain           ResolutionChain
	definitions     []Definition
	definitionsByID map[string]runtimeDefinitionContract
	policies        map[SettingClass]ClassPolicy
	provenance      Provenance
	refreshTimeout  time.Duration
	clock           RuntimeClock
	executor        RefreshExecutor
	state           atomic.Pointer[runtimeState]

	refreshMu sync.Mutex
	refresh   *refreshFlight

	refreshInterval      time.Duration
	maxJitter            time.Duration
	jitter               func(time.Duration) time.Duration
	invalidations        InvalidationSource
	watchBuffer          int
	reconnectDelay       time.Duration
	invalidationDebounce time.Duration
	snapshotStore        SnapshotStore
	triggers             chan struct{}

	lifecycleMu sync.Mutex
	starting    chan struct{}
	started     bool
	closed      bool
	cancel      context.CancelFunc
	stopped     chan struct{}
	waiters     sync.WaitGroup
}

// NewRuntime checks provider capabilities and validates definitions/configuration
// before reading or writing settings or starting goroutines. It copies the
// definition slice, policy map and encoded defaults, not collaborator objects.
// There must be 1–10000 definitions, a valid chain and explicit class policies.
// Defaults/callbacks may run during validation; errors prevent construction.
func NewRuntime(config RuntimeConfig) (*Runtime, error) {
	if config.Provider == nil {
		return nil, fmt.Errorf("settings: runtime provider is required")
	}
	if !config.Provider.Capabilities().Snapshots {
		return nil, fmt.Errorf("settings: runtime provider must support atomic snapshots")
	}
	if err := config.Chain.validate(); err != nil {
		return nil, err
	}
	if len(config.Definitions) == 0 || len(config.Definitions) > 10_000 {
		return nil, fmt.Errorf("settings: runtime definitions must be between 1 and 10000")
	}
	if config.RefreshTimeout <= 0 || config.RefreshTimeout > 5*time.Minute {
		return nil, fmt.Errorf("settings: refresh timeout must be between 1ns and 5m")
	}
	if !validProvenance(config.Provenance) || config.Provenance == ProvenanceDefaults {
		return nil, fmt.Errorf("settings: invalid runtime provenance")
	}
	if config.RefreshInterval < 0 || config.RefreshInterval > 24*time.Hour ||
		(config.RefreshInterval > 0 && config.RefreshInterval < 10*time.Millisecond) {
		return nil, fmt.Errorf("settings: invalid refresh interval")
	}
	if config.MaxJitter < 0 || config.MaxJitter > time.Hour || (config.MaxJitter > 0 && config.Jitter == nil) {
		return nil, fmt.Errorf("settings: invalid refresh jitter")
	}
	if config.Invalidations != nil {
		if config.WatchBuffer < 1 || config.WatchBuffer > 10_000 {
			return nil, fmt.Errorf("settings: watcher buffer must be between 1 and 10000")
		}
		if config.ReconnectDelay < 10*time.Millisecond || config.ReconnectDelay > time.Minute {
			return nil, fmt.Errorf("settings: reconnect delay must be between 10ms and 1m")
		}
		if config.InvalidationDebounce < 10*time.Millisecond || config.InvalidationDebounce > time.Minute {
			return nil, fmt.Errorf("settings: invalid invalidation debounce")
		}
	}

	definitions := append([]Definition(nil), config.Definitions...)
	definitionsByID := make(map[string]runtimeDefinitionContract, len(definitions))
	classes := make(map[SettingClass]struct{}, 3)
	for _, definition := range definitions {
		if definition == nil {
			return nil, fmt.Errorf("settings: nil runtime definition")
		}
		if err := definition.ValidateDefinition(); err != nil {
			return nil, err
		}
		if _, duplicate := definitionsByID[definition.StableID()]; duplicate {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateDefinition, definition.StableID())
		}
		defaultData, hasDefault, err := definition.DefaultEncoded()
		if err != nil {
			return nil, fmt.Errorf("%w: runtime default for %s", ErrInvalidDefinition, definition.StableID())
		}
		if hasDefault {
			if err := definition.ValidateEncoded(defaultData); err != nil {
				return nil, fmt.Errorf("%w: runtime default for %s", ErrInvalidDefinition, definition.StableID())
			}
			defaultData = append([]byte(nil), defaultData...)
		}
		definitionsByID[definition.StableID()] = runtimeDefinitionContract{
			definition: definition, defaultData: defaultData, hasDefault: hasDefault,
		}
		classes[ClassOf(definition)] = struct{}{}
	}
	policies := make(map[SettingClass]ClassPolicy, len(config.Policies))
	for class, policy := range config.Policies {
		if err := validateClassPolicy(class, policy); err != nil {
			return nil, err
		}
		policies[class] = policy
	}
	for class := range classes {
		if _, ok := policies[class]; !ok {
			return nil, fmt.Errorf("settings: missing policy for class %d", class)
		}
	}

	clock := config.Clock
	if clock == nil {
		clock = systemRuntimeClock{}
	}
	executor := config.Executor
	if executor == nil {
		executor = directRefreshExecutor{}
	}
	return &Runtime{
		provider: config.Provider, chain: config.Chain, definitions: definitions,
		definitionsByID: definitionsByID,
		policies:        policies, provenance: config.Provenance,
		refreshTimeout: config.RefreshTimeout, clock: clock, executor: executor,
		refreshInterval: config.RefreshInterval, maxJitter: config.MaxJitter,
		jitter: config.Jitter, invalidations: config.Invalidations,
		watchBuffer: config.WatchBuffer, reconnectDelay: config.ReconnectDelay,
		invalidationDebounce: config.InvalidationDebounce,
		snapshotStore:        config.SnapshotStore, triggers: make(chan struct{}, 1),
	}, nil
}

func validateClassPolicy(class SettingClass, policy ClassPolicy) error {
	if class != ClassStandard && class != ClassSecret && class != ClassSecuritySensitive {
		return fmt.Errorf("settings: invalid policy class %d", class)
	}
	if policy.FreshFor < 0 || policy.MaxStaleness < policy.FreshFor || policy.MaxStaleness > 24*time.Hour {
		return fmt.Errorf("settings: invalid staleness bounds for class %d", class)
	}
	if !validAvailabilityAction(policy.OnUnavailable) || !validAvailabilityAction(policy.OnStale) ||
		!validAvailabilityAction(policy.OnExpired) {
		return fmt.Errorf("settings: invalid availability action for class %d", class)
	}
	if policy.OnUnavailable == ServeLastKnownGood || policy.OnExpired == ServeLastKnownGood {
		return fmt.Errorf("settings: class %d permits unbounded stale state", class)
	}
	return nil
}

func validAvailabilityAction(action AvailabilityAction) bool {
	return action == ServeLastKnownGood || action == UseDefault || action == FailClosed
}

// Refresh coalesces concurrent callers into one bounded complete capture. A
// failed or malformed capture never replaces the last-known-good snapshot.
// The first caller owns the shared flight's bounded context; another caller can
// stop waiting but does not cancel that flight. A cache Save/encoding failure can
// occur after the new in-memory snapshot is published; an error is not proof
// that state stayed unchanged. Context bounds require cooperative collaborators.
func (runtime *Runtime) Refresh(ctx context.Context) error {
	return runtime.refreshSnapshot(ctx, nil)
}

func (runtime *Runtime) refreshSnapshot(ctx context.Context, fence *Record) error {
	runtime.refreshMu.Lock()
	if flight := runtime.refresh; flight != nil {
		runtime.refreshMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-flight.done:
			if flight.err != nil {
				return flight.err
			}
			if fence == nil {
				return nil
			}
			current := runtime.state.Load()
			if current != nil && runtime.validateReplacement(current.snapshot, fence) == nil {
				return nil
			}
			return runtime.refreshSnapshot(ctx, fence)
		}
	}
	flight := &refreshFlight{done: make(chan struct{})}
	runtime.refresh = flight
	runtime.refreshMu.Unlock()

	refreshCtx, cancel := context.WithTimeout(ctx, runtime.refreshTimeout)
	var candidate Snapshot
	err := runtime.executor.Execute(refreshCtx, func(operationCtx context.Context) error {
		snapshot, captureErr := CaptureWithOptions(operationCtx, runtime.provider, runtime.chain, CaptureOptions{
			CapturedAt: runtime.clock.Now(), Provenance: runtime.provenance,
		}, runtime.definitions...)
		if captureErr != nil {
			return captureErr
		}
		candidate = snapshot
		return nil
	})
	if err == nil {
		err = runtime.validateReplacement(candidate, fence)
	}
	// Publish validated memory before persistence: a cache failure must not undo
	// an already accepted durable view or be mistaken for no activation.
	if err == nil {
		runtime.state.Store(&runtimeState{snapshot: candidate})
		if runtime.snapshotStore != nil {
			data, encodeErr := candidate.MarshalBinary()
			if encodeErr != nil {
				err = encodeErr
			} else if saveErr := runtime.snapshotStore.Save(refreshCtx, data); saveErr != nil {
				err = fmt.Errorf("settings: save snapshot cache: %w", saveErr)
			}
		}
	}
	cancel()

	runtime.refreshMu.Lock()
	flight.err = err
	runtime.refresh = nil
	close(flight.done)
	runtime.refreshMu.Unlock()
	return err
}

// A retained tombstone is a version fence, not disposable absence. Rejecting
// disappearance/lower versions prevents resurrecting stale overrides.
func (runtime *Runtime) validateReplacement(candidate Snapshot, fence *Record) error {
	if current := runtime.state.Load(); current != nil {
		for coordinate, previous := range current.snapshot.records {
			next, ok := candidate.records[coordinate]
			if !ok || next.Version < previous.Version {
				return ErrNonMonotonicSnapshot
			}
		}
	}
	if fence == nil {
		return nil
	}
	record, ok := candidate.records[snapshotCoordinate{scope: fence.Scope, key: fence.Key}]
	if !ok || record.Version < fence.Version {
		return ErrNonMonotonicSnapshot
	}
	if record.Version == fence.Version && (record.State != fence.State || string(record.Data) != string(fence.Data)) {
		return ErrNonMonotonicSnapshot
	}
	return nil
}

// Apply commits one durable mutation and then refreshes the complete snapshot
// with the acknowledged record as a monotonic fence. Only registered definitions
// and chain owners are admitted. A positive-version acknowledgement is treated
// as committed even when provider fanout fails; errors.Join can contain committed
// provider and reconciliation failures. Inspect the record and error before any
// retry. Successful reconciliation requires the complete snapshot to include
// that acknowledged version or a newer one.
func (runtime *Runtime) Apply(ctx context.Context, mutation Mutation) (Record, error) {
	if err := runtime.validateMutation(mutation); err != nil {
		return Record{}, err
	}
	record, writeErr := runtime.provider.Apply(ctx, mutation)
	if record.Version == 0 {
		if writeErr == nil {
			writeErr = fmt.Errorf("%w: provider returned an empty write acknowledgement", ErrInvalidValue)
		}
		return record, writeErr
	}
	if writeErr != nil {
		var committed *CommittedWriteError
		if !errors.As(writeErr, &committed) {
			writeErr = &CommittedWriteError{Operation: "provider fanout", Committed: true, Err: writeErr}
		}
	}
	refreshErr := runtime.refreshSnapshot(ctx, &record)
	if refreshErr != nil {
		refreshErr = &CommittedWriteError{Operation: "snapshot reconciliation", Committed: true, Err: refreshErr}
	}
	return record, errors.Join(writeErr, refreshErr)
}

func (runtime *Runtime) validateMutation(mutation Mutation) error {
	if err := ValidateMutation(mutation); err != nil {
		return err
	}
	contract, ok := runtime.definitionsByID[mutation.Key]
	if !ok || !chainContains(runtime.chain, mutation.Scope) ||
		contract.definition.CodecID() != mutation.CodecID ||
		contract.definition.CodecVersion() != mutation.CodecVersion ||
		contract.definition.Sensitive() != mutation.Sensitive {
		return fmt.Errorf("%w: runtime definition contract for %s", ErrInvalidMutation, mutation.Key)
	}
	if mutation.Action == ActionSet {
		if err := contract.definition.ValidateEncoded(mutation.Data); err != nil {
			return fmt.Errorf("%w: runtime value for %s", ErrInvalidMutation, mutation.Key)
		}
	}
	return nil
}

// Freshness identifies whether a runtime result came from fresh state, a
// bounded last-known-good snapshot, or a definition default.
type Freshness uint8

const (
	// Fresh labels a result within the fresh threshold.
	Fresh Freshness = iota + 1
	// Stale labels a permitted bounded last-known-good result.
	Stale
	// Default labels policy-driven definition fallback.
	Default
)

// RuntimeResult combines typed resolution provenance with snapshot freshness.
type RuntimeResult[T any] struct {
	// Result is the typed resolution outcome and provenance.
	Result[T]
	// Snapshot carries selected capture identity and age; default results have default provenance.
	Snapshot SnapshotMetadata
	// Freshness records policy selection independently of resolution Status.
	Freshness Freshness
}

// ResolveCurrent applies the key's explicit class policy before resolving the
// immutable current snapshot.
func ResolveCurrent[T any](runtime *Runtime, key Key[T]) (RuntimeResult[T], error) {
	definition, registeredKey, err := runtimeDefinition(runtime, key)
	if err != nil {
		return RuntimeResult[T]{}, err
	}
	key = registeredKey
	now := runtime.clock.Now()
	state := runtime.state.Load()
	policy := runtime.policies[ClassOf(definition)]
	if state == nil {
		return resolveAvailability(runtime, key, policy.OnUnavailable, ErrSnapshotUnavailable, now)
	}
	metadata := state.snapshot.Metadata(now)
	if metadata.Age <= policy.FreshFor {
		return resolveRuntimeSnapshot(runtime, state.snapshot, key, metadata, Fresh)
	}
	if metadata.Age <= policy.MaxStaleness {
		if policy.OnStale == ServeLastKnownGood {
			return resolveRuntimeSnapshot(runtime, state.snapshot, key, metadata, Stale)
		}
		return resolveAvailability(runtime, key, policy.OnStale, ErrSnapshotStale, now)
	}
	return resolveAvailability(runtime, key, policy.OnExpired, ErrSnapshotExpired, now)
}

func runtimeDefinition[T any](runtime *Runtime, key Key[T]) (Definition, Key[T], error) {
	if err := key.ValidateDefinition(); err != nil {
		return nil, Key[T]{}, err
	}
	contract, ok := runtime.definitionsByID[key.StableID()]
	if !ok || contract.definition.CodecID() != key.CodecID() || contract.definition.CodecVersion() != key.CodecVersion() ||
		contract.definition.Sensitive() != key.Sensitive() || ClassOf(contract.definition) != ClassOf(key) {
		return nil, Key[T]{}, fmt.Errorf("%w: runtime definition contract for %s", ErrInvalidDefinition, key.StableID())
	}
	registeredKey := key
	registeredKey.hasDefault = contract.hasDefault
	if contract.hasDefault {
		value, decodeErr := key.codec.Decode(contract.defaultData)
		if decodeErr != nil || key.validateValue(value) != nil {
			return nil, Key[T]{}, fmt.Errorf("%w: runtime default for %s", ErrInvalidDefinition, key.StableID())
		}
		registeredKey.defaultValue = value
	}
	return contract.definition, registeredKey, nil
}

func resolveRuntimeSnapshot[T any](runtime *Runtime, snapshot Snapshot, key Key[T], metadata SnapshotMetadata, freshness Freshness) (RuntimeResult[T], error) {
	result, err := ResolveSnapshot(snapshot, key, runtime.chain)
	return RuntimeResult[T]{Result: result, Snapshot: metadata, Freshness: freshness}, err
}

func resolveAvailability[T any](runtime *Runtime, key Key[T], action AvailabilityAction, failure error, now time.Time) (RuntimeResult[T], error) {
	if action != UseDefault {
		return RuntimeResult[T]{}, failure
	}
	if !key.hasDefault {
		return RuntimeResult[T]{}, ErrDefaultUnavailable
	}
	result, resolveErr := ResolveSnapshot(Snapshot{}, key, runtime.chain)
	metadata := SnapshotMetadata{Provenance: ProvenanceDefaults, CapturedAt: now}
	return RuntimeResult[T]{Result: result, Snapshot: metadata, Freshness: Default}, resolveErr
}

// Ready reports whether every registered definition can currently be served
// under its own class policy. It checks age/default availability without IO or
// value resolution and is not a started/running/healthy-connection indicator.
// A still-usable snapshot may remain ready after Shutdown.
func (runtime *Runtime) Ready() bool {
	now := runtime.clock.Now()
	state := runtime.state.Load()
	for _, definition := range runtime.definitions {
		policy := runtime.policies[ClassOf(definition)]
		action := policy.OnUnavailable
		if state != nil {
			age := state.snapshot.Metadata(now).Age
			switch {
			case age <= policy.FreshFor:
				continue
			case age <= policy.MaxStaleness:
				action = policy.OnStale
			default:
				action = policy.OnExpired
			}
		}
		if action == ServeLastKnownGood && state != nil {
			continue
		}
		if action != UseDefault {
			return false
		}
		if !runtime.definitionsByID[definition.StableID()].hasDefault {
			return false
		}
	}
	return true
}

// InvalidationProtocolVersion is the current hint envelope understood by
// Runtime. Unknown versions cause durable reconciliation instead of rejection.
const InvalidationProtocolVersion uint16 = 1

// Invalidation is a versioned at-most-once hint. It never carries setting data
// and is never applied directly to a snapshot.
type Invalidation struct {
	// ProtocolVersion is the hint envelope edition; unknown editions trigger durable refresh.
	ProtocolVersion uint16 `json:"protocol_version"`
	// Scope identifies the hinted coordinate; malformed hints also request refresh.
	Scope Scope `json:"scope"`
	// Key is the hinted stable key identifier; no setting value belongs here.
	Key string `json:"key"`
	// Version is a hint compared only with captured durable versions, not trusted as a future fence.
	Version uint64 `json:"version"`
	// State is a hint state; snapshots are never mutated directly from hints.
	State State `json:"state"`
}

// InvalidationSource opens one bounded, cancellable subscription. A closed
// channel requests a bounded reconnect.
type InvalidationSource interface {
	// Watch returns context-owned event/error channels; close on termination and honor bounded
	// buffering.
	Watch(context.Context, int) (<-chan Invalidation, <-chan error, error)
}

// Start attempts cached recovery and durable capture, then starts reconciliation
// and invalidation watching if class policies permit serving state. The supplied
// context owns the background-loop lifetime; cancel it or call Shutdown to
// stop the runtime. Failed durable refresh may still permit startup from a
// policy-usable cached snapshot or defaults. Failed startup can be retried;
// successful startup cannot be repeated, and Shutdown permanently refuses Start.
// The context must be nonnil and collaborators must honor cancellation.
func (runtime *Runtime) Start(ctx context.Context) error {
	runtime.lifecycleMu.Lock()
	if runtime.closed {
		runtime.lifecycleMu.Unlock()
		return ErrRuntimeClosed
	}
	if runtime.started {
		runtime.lifecycleMu.Unlock()
		return ErrRuntimeStarted
	}
	if starting := runtime.starting; starting != nil {
		runtime.lifecycleMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-starting:
			return ErrRuntimeStarted
		}
	}
	starting := make(chan struct{})
	runtime.starting = starting
	runtime.lifecycleMu.Unlock()

	cacheErr := runtime.loadCachedSnapshot(ctx)
	if !waitRuntime(ctx, runtime.withJitter(0)) {
		runtime.finishStarting(starting, false)
		return ctx.Err()
	}
	err := runtime.Refresh(ctx)
	if err != nil && !runtime.Ready() {
		runtime.finishStarting(starting, false)
		return errors.Join(cacheErr, err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		runtime.finishStarting(starting, false)
		return contextErr
	}

	runCtx, cancel := context.WithCancel(ctx)
	runtime.lifecycleMu.Lock()
	runtime.cancel = cancel
	runtime.stopped = make(chan struct{})
	runtime.started = true
	if runtime.refreshInterval > 0 || runtime.invalidations != nil {
		runtime.waiters.Add(1)
	}
	if runtime.invalidations != nil {
		runtime.waiters.Add(1)
	}
	runtime.starting = nil
	close(starting)
	runtime.lifecycleMu.Unlock()

	if runtime.refreshInterval > 0 || runtime.invalidations != nil {
		go runtime.runRefreshLoop(runCtx)
	}
	if runtime.invalidations != nil {
		go runtime.runWatchLoop(runCtx)
	}
	go func() {
		runtime.waiters.Wait()
		close(runtime.stopped)
	}()
	return nil
}

func (runtime *Runtime) loadCachedSnapshot(ctx context.Context) error {
	if runtime.snapshotStore == nil {
		return nil
	}
	data, ok, err := runtime.snapshotStore.Load(ctx)
	if err != nil {
		return fmt.Errorf("settings: load snapshot cache: %w", err)
	}
	if !ok {
		return ErrSnapshotUnavailable
	}
	snapshot, err := RestoreSnapshot(data, runtime.chain, runtime.definitions...)
	if err != nil {
		return err
	}
	if err := runtime.validateReplacement(snapshot, nil); err != nil {
		return err
	}
	runtime.state.Store(&runtimeState{snapshot: snapshot})
	return nil
}

func (runtime *Runtime) finishStarting(starting chan struct{}, started bool) {
	runtime.lifecycleMu.Lock()
	runtime.started = started
	runtime.starting = nil
	close(starting)
	runtime.lifecycleMu.Unlock()
}

// Shutdown cancels watcher, reconnect, periodic refresh, and in-flight
// background refresh work, then waits for every owned goroutine to drain. It
// is safe to call concurrently or repeatedly. Each caller's context bounds
// only that caller's wait after shutdown begins; cancellation continues after
// a caller stops waiting. If the context ends while Start is still in progress,
// Shutdown returns without initiating shutdown and a later call is required.
// After the complete drain, Shutdown returns nil regardless of context state.
func (runtime *Runtime) Shutdown(ctx context.Context) error {
	runtime.lifecycleMu.Lock()
	if starting := runtime.starting; starting != nil {
		runtime.lifecycleMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-starting:
			return runtime.Shutdown(ctx)
		}
	}
	if !runtime.started {
		runtime.closed = true
		stopped := runtime.stopped
		runtime.lifecycleMu.Unlock()
		if stopped == nil {
			return nil
		}
		return waitForRuntimeShutdown(ctx, stopped)
	}
	runtime.closed = true
	runtime.started = false
	cancel := runtime.cancel
	stopped := runtime.stopped
	runtime.lifecycleMu.Unlock()
	cancel()
	return waitForRuntimeShutdown(ctx, stopped)
}

func waitForRuntimeShutdown(ctx context.Context, stopped <-chan struct{}) error {
	select {
	case <-stopped:
		return nil
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stopped:
		return nil
	}
}

// Close delegates to Shutdown.
//
// Deprecated: use Shutdown. Close retains its context-aware signature for
// compatibility with v1 consumers.
func (runtime *Runtime) Close(ctx context.Context) error {
	return runtime.Shutdown(ctx)
}

func (runtime *Runtime) runRefreshLoop(ctx context.Context) {
	defer runtime.waiters.Done()
	for {
		var timer *time.Timer
		var timerChannel <-chan time.Time
		if runtime.refreshInterval > 0 {
			timer = time.NewTimer(runtime.withJitter(runtime.refreshInterval))
			timerChannel = timer.C
		}
		triggered := false
		select {
		case <-ctx.Done():
			if timer != nil {
				stopTimer(timer)
			}
			return
		case <-runtime.triggers:
			triggered = true
			if timer != nil {
				stopTimer(timer)
			}
		case <-timerChannel:
		}
		if triggered && !waitRuntime(ctx, runtime.withJitter(runtime.invalidationDebounce)) {
			return
		}
		_ = runtime.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
	}
}

func (runtime *Runtime) runWatchLoop(ctx context.Context) {
	defer runtime.waiters.Done()
	for {
		events, errorsOut, err := runtime.invalidations.Watch(ctx, runtime.watchBuffer)
		if err == nil {
			runtime.consumeInvalidations(ctx, events, errorsOut)
		}
		if ctx.Err() != nil {
			return
		}
		if !waitRuntime(ctx, runtime.withJitter(runtime.reconnectDelay)) {
			return
		}
	}
}

func (runtime *Runtime) consumeInvalidations(ctx context.Context, events <-chan Invalidation, errorsOut <-chan error) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-errorsOut:
			if !ok {
				return
			}
			if err != nil {
				runtime.requestRefresh()
			}
		case event, ok := <-events:
			if !ok {
				return
			}
			if runtime.acceptInvalidation(event) {
				runtime.requestRefresh()
			}
		}
	}
}

func (runtime *Runtime) requestRefresh() {
	select {
	case runtime.triggers <- struct{}{}:
	default:
	}
}

// Hints never become an authoritative version fence. Retaining untrusted future
// versions could suppress later real changes; compare only with captured state.
func (runtime *Runtime) acceptInvalidation(event Invalidation) bool {
	if event.ProtocolVersion != InvalidationProtocolVersion {
		return true
	}
	if event.Version == 0 {
		return true
	}
	if event.Key == "" {
		return true
	}
	if event.Scope.Validate() != nil {
		return true
	}
	if event.State != StateMissing && event.State != StateValue && event.State != StateCleared {
		return true
	}
	coordinate := snapshotCoordinate{scope: event.Scope, key: event.Key}
	if current := runtime.state.Load(); current != nil {
		if record, ok := current.snapshot.records[coordinate]; ok && record.Version >= event.Version {
			return false
		}
	}
	return true
}

func (runtime *Runtime) withJitter(base time.Duration) time.Duration {
	if runtime.maxJitter == 0 {
		return base
	}
	jitter := runtime.jitter(runtime.maxJitter)
	jitter = min(max(jitter, 0), runtime.maxJitter)
	return base + jitter
}

func waitRuntime(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		stopTimer(timer)
		return false
	case <-timer.C:
		return true
	}
}

func stopTimer(timer *time.Timer) {
	timer.Stop()
}

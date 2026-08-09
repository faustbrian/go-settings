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
	ErrSnapshotUnavailable  = errors.New("settings: snapshot unavailable")
	ErrSnapshotStale        = errors.New("settings: snapshot stale")
	ErrSnapshotExpired      = errors.New("settings: snapshot expired")
	ErrDefaultUnavailable   = errors.New("settings: default unavailable")
	ErrRuntimeStarted       = errors.New("settings: runtime already started")
	ErrRuntimeClosed        = errors.New("settings: runtime closed")
	ErrNonMonotonicSnapshot = errors.New("settings: non-monotonic snapshot")
)

// CommittedWriteError reports that durable state advanced even though
// reconciliation or cache fanout did not complete.
type CommittedWriteError struct {
	Operation string
	Committed bool
	Err       error
}

func (err *CommittedWriteError) Error() string {
	return fmt.Sprintf("settings %s after durable commit: %v", err.Operation, err.Err)
}

func (err *CommittedWriteError) Unwrap() error { return err.Err }

// AvailabilityAction defines the only allowed response to unavailable or
// non-fresh state.
type AvailabilityAction uint8

const (
	ServeLastKnownGood AvailabilityAction = iota + 1
	UseDefault
	FailClosed
)

// ClassPolicy bounds how long one setting class may use a last-known-good
// snapshot and explicitly selects each degradation outcome.
type ClassPolicy struct {
	FreshFor      time.Duration
	MaxStaleness  time.Duration
	OnUnavailable AvailabilityAction
	OnStale       AvailabilityAction
	OnExpired     AvailabilityAction
}

// RuntimeClock supplies freshness time without hiding a process-global clock.
type RuntimeClock interface{ Now() time.Time }

type systemRuntimeClock struct{}

func (systemRuntimeClock) Now() time.Time { return time.Now() }

// RefreshExecutor composes retry, circuit-breaker, bulkhead, adaptive, and
// shared-budget policies around one complete refresh attempt.
type RefreshExecutor interface {
	Execute(context.Context, func(context.Context) error) error
}

// RefreshExecutorFunc adapts a policy-composed function to RefreshExecutor.
type RefreshExecutorFunc func(context.Context, func(context.Context) error) error

func (executor RefreshExecutorFunc) Execute(ctx context.Context, operation func(context.Context) error) error {
	return executor(ctx, operation)
}

type directRefreshExecutor struct{}

func (directRefreshExecutor) Execute(ctx context.Context, operation func(context.Context) error) error {
	return operation(ctx)
}

// SnapshotStore persists an encrypted, caller-owned last-known-good document
// for cold-start recovery. Implementations must bound their IO by context.
type SnapshotStore interface {
	Load(context.Context) ([]byte, bool, error)
	Save(context.Context, []byte) error
}

// RuntimeConfig defines the durable source, immutable coordinate set, explicit
// class policies, and bound for one logical refresh.
type RuntimeConfig struct {
	Provider             Provider
	Chain                ResolutionChain
	Definitions          []Definition
	Policies             map[SettingClass]ClassPolicy
	Provenance           Provenance
	RefreshTimeout       time.Duration
	Clock                RuntimeClock
	Executor             RefreshExecutor
	RefreshInterval      time.Duration
	MaxJitter            time.Duration
	Jitter               func(time.Duration) time.Duration
	Invalidations        InvalidationSource
	WatchBuffer          int
	ReconnectDelay       time.Duration
	InvalidationDebounce time.Duration
	SnapshotStore        SnapshotStore
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

// NewRuntime validates all policies before any provider operation or goroutine
// can start.
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
// with the acknowledged record as a monotonic fence.
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
	Fresh Freshness = iota + 1
	Stale
	Default
)

// RuntimeResult combines typed resolution provenance with snapshot freshness.
type RuntimeResult[T any] struct {
	Result[T]
	Snapshot  SnapshotMetadata
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
// under its own class policy.
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
	ProtocolVersion uint16 `json:"protocol_version"`
	Scope           Scope  `json:"scope"`
	Key             string `json:"key"`
	Version         uint64 `json:"version"`
	State           State  `json:"state"`
}

// InvalidationSource opens one bounded, cancellable subscription. A closed
// channel requests a bounded reconnect.
type InvalidationSource interface {
	Watch(context.Context, int) (<-chan Invalidation, <-chan error, error)
}

// Start obtains an initial last-known-good snapshot before reporting ready,
// then starts periodic reconciliation and invalidation watching. The supplied
// context owns the background-loop lifetime; cancel it or call Close to stop
// the runtime.
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
	runtime.starting = nil
	close(starting)
	runtime.lifecycleMu.Unlock()

	if runtime.refreshInterval > 0 || runtime.invalidations != nil {
		runtime.waiters.Add(1)
		go runtime.runRefreshLoop(runCtx)
	}
	if runtime.invalidations != nil {
		runtime.waiters.Add(1)
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

// Close cancels watcher, reconnect, periodic refresh, and in-flight background
// refresh work, then waits for every owned goroutine to drain.
func (runtime *Runtime) Close(ctx context.Context) error {
	runtime.lifecycleMu.Lock()
	if starting := runtime.starting; starting != nil {
		runtime.lifecycleMu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-starting:
			return runtime.Close(ctx)
		}
	}
	if !runtime.started {
		runtime.closed = true
		runtime.lifecycleMu.Unlock()
		return nil
	}
	runtime.closed = true
	runtime.started = false
	cancel := runtime.cancel
	stopped := runtime.stopped
	runtime.lifecycleMu.Unlock()
	cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-stopped:
		return nil
	}
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
	watermarks := make(map[snapshotCoordinate]uint64)
	for {
		events, errorsOut, err := runtime.invalidations.Watch(ctx, runtime.watchBuffer)
		if err == nil {
			runtime.consumeInvalidations(ctx, events, errorsOut, watermarks)
		}
		if ctx.Err() != nil {
			return
		}
		if !waitRuntime(ctx, runtime.withJitter(runtime.reconnectDelay)) {
			return
		}
	}
}

func (runtime *Runtime) consumeInvalidations(ctx context.Context, events <-chan Invalidation, errorsOut <-chan error, watermarks map[snapshotCoordinate]uint64) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-errorsOut:
			if !ok {
				return
			}
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if runtime.acceptInvalidation(event, watermarks) {
				select {
				case runtime.triggers <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (runtime *Runtime) acceptInvalidation(event Invalidation, watermarks map[snapshotCoordinate]uint64) bool {
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
	if event.Version <= watermarks[coordinate] {
		return false
	}
	watermarks[coordinate] = event.Version
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

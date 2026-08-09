# Runtime fleet resilience

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as shown
here.

`Runtime` owns one immutable last-known-good snapshot per process. PostgreSQL
remains the durability and ordering boundary. Valkey and invalidation delivery
are repair accelerators; neither is authoritative. Kubernetes replicas do not
share process memory and MUST independently enforce the same definitions and
class policies.

## Snapshot contract

A current snapshot contains the complete configured coordinate set returned by
one snapshot-capable provider bulk read. Every record is decoded and validated
for scope, key, state, codec identity and version, value size, value semantics,
record version, and update time before the snapshot is atomically published.
Unexpected or duplicate coordinates fail the whole capture. A failed capture
MUST NOT alter the current snapshot.

Each snapshot reports a content revision, capture time, provenance, original
provenance after cache restore, and non-negative age. Reads retain the selected
snapshot for their entire resolution. State and byte slices are copied at the
provider boundary, so later provider or caller mutation cannot change a current
snapshot.

`SnapshotStore` is an OPTIONAL cold-start optimization. Its document is
versioned, size-bounded to 2 MiB, strict about unknown fields, and checked
against the configured definitions and content revision. The wire format is
not encrypted or authenticated. A production store containing secrets MUST
provide authenticated encryption, bounded I/O, atomic replacement, and
restricted access outside this package.

## Required class policy

Every registered setting class MUST have explicit `FreshFor`, `MaxStaleness`,
`OnUnavailable`, `OnStale`, and `OnExpired` values. `MaxStaleness` is an
absolute per-read bound; `ServeLastKnownGood` is rejected for unavailable or
expired state. A production deployment SHOULD start from this profile:

`ResolveCurrent` accepts only keys whose stable ID, codec contract, sensitivity,
and class match a registered definition. Defaults are captured from that
registered definition during runtime construction. Reconstructing a key cannot
weaken its class policy or substitute a caller-selected default.

| Class | Unavailable | Between fresh and maximum age | Beyond maximum age |
| --- | --- | --- | --- |
| Standard | `UseDefault` only with an operationally safe default; otherwise `FailClosed` | `ServeLastKnownGood` | `UseDefault` or `FailClosed` |
| Secret | `FailClosed` | `FailClosed` | `FailClosed` |
| Security-sensitive | deny-safe `UseDefault` or `FailClosed` | deny-safe `UseDefault` or `FailClosed` | deny-safe `UseDefault` or `FailClosed` |

Security-sensitive defaults MUST deny access or disable the protected action.
Secret values SHOULD use `WithSensitive` plus an `EncryptionCodec`; a cached
secret MUST NOT be accepted merely because the cache document is syntactically
valid. Applications MAY choose a different bounded policy after documenting
the setting-specific safety argument.

## Cold start and readiness

`Start` serializes concurrent starts and performs these steps:

1. load and completely validate the OPTIONAL cached snapshot;
2. wait the configured bounded startup jitter;
3. run one bounded durable refresh through the supplied executor;
4. report success only when `Ready` can serve every definition under its class
   policy; and
5. start the periodic refresh and watcher loops.

The context passed to `Start` owns those background loops. Applications MUST
derive it from the pod or process lifetime, not from a startup probe or request;
cancelling it stops periodic reconciliation and invalidation watching. `Close`
also cancels that lifetime and bounds the drain with its own caller context.

| Cached snapshot | PostgreSQL or durable provider | Result |
| --- | --- | --- |
| absent, malformed, or unavailable | complete refresh succeeds | start with the durable snapshot |
| valid and within policy | unavailable or refresh fails | start from cached last-known-good state |
| stale but within every class policy | unavailable or refresh fails | start with each class's stale/default/fail-closed behavior |
| expired for a class | unavailable or refresh fails | start only if that class can use an available default; otherwise remain unready |
| absent | unavailable | start only if every class permits and has a usable default; otherwise fail startup |

A Valkey outage follows the configured Valkey `Bypass` or `FailClosed` policy.
With `Bypass`, the durable bulk read remains authoritative. With `FailClosed`,
a cache-fill failure fails that refresh; an already-valid snapshot may still
keep the pod ready within class policy.

The readiness probe SHOULD call `Ready`. The process MUST NOT advertise
readiness based only on successful construction or watcher connectivity.
Liveness SHOULD remain independent of PostgreSQL and Valkey so a temporary
dependency outage does not restart every pod.

## Refresh, convergence, and invalidations

Only one complete refresh runs in a process at a time. Concurrent callers join
that flight, and each caller retains its own cancellation. The entire executor,
including retries, is bounded by `RefreshTimeout`. Periodic refresh, startup
jitter, invalidation debounce, watcher buffers, and reconnect waits all have
validated upper bounds and stop with their owning context.

Invalidations are versioned, data-free, at-most-once hints. A valid newer hint
advances the process-local watermark and requests a durable refresh. Duplicate
and reordered versions are dropped. Invalid envelopes and unknown protocol
versions request a full durable refresh, which makes mixed-version rollouts
safe. A closed or failed subscription reconnects after bounded jitter. A lost
hint is repaired by periodic refresh.

For a healthy dependency path, the observable remote-pod convergence window is
bounded by:

```text
invalidation path <= delivery + debounce + jitter + refresh timeout
lost-event path   <= refresh interval + jitter + refresh timeout
```

The effective serving bound is the smaller of successful convergence and the
class's `MaxStaleness`; after that age the configured default or fail-closed
behavior applies. Operators MUST choose intervals and policy ages so these
windows meet the application's consistency requirement.

Valkey cache writes use an atomic version comparison. Delayed completion of an
older fill cannot overwrite a newer value or tombstone. TTL expiration and
durable periodic refresh provide independent repair paths.

## Writes and monotonic reads

`Runtime.Apply` commits through the provider, then performs a complete refresh
fenced by the acknowledged record. A successful return guarantees same-pod
read-after-write and prevents the current snapshot from moving to an older
version. A provider acknowledgement that is empty or conflicts with the
refreshed record is an error.

Before commit, the runtime rejects malformed mutations, scopes outside its
resolution chain, unregistered keys, codec or sensitivity mismatches, and
values that fail the registered definition. These failures are never reported
as committed writes.

If durability succeeded but Valkey fanout, snapshot persistence, or
reconciliation failed, the returned `CommittedWriteError` has
`Committed=true`. Callers MUST treat that result as an acknowledged durable
write, MUST NOT blindly retry a non-idempotent operation, and SHOULD reconcile
by version. The prior last-known-good snapshot remains readable within policy.
An abrupt pod loss after PostgreSQL commit therefore cannot erase the write;
other pods converge through invalidation or periodic durable refresh.

Monotonic reads are guaranteed within one live `Runtime` instance. They are not
guaranteed across an arbitrary client hop to a newly started pod unless session
routing or a caller-supplied version fence is used. Cross-pod eventual
convergence remains bounded as above.

## Kubernetes lifecycle

- **Scale-up:** replicas SHOULD use independent bounded jitter to distribute
  initial PostgreSQL reads. Single-flight prevents amplification inside each
  pod; it is not a cluster lock.
- **Rollout:** old and new pods MUST retain compatible durable schemas and codec
  contracts. Unknown invalidation protocol versions cause reconciliation.
  Cached documents with unknown schema or incompatible definitions are
  rejected. Breaking definition changes require an explicit migration and a
  rollout plan.
- **PostgreSQL failover:** the bounded executor MAY apply the shared retry,
  breaker, bulkhead, adaptive, and budget policies. Existing snapshots continue
  only within class policy. Recovery refreshes atomically replace them.
- **Valkey outage:** invalidation acceleration and bounded-stale point reads may
  degrade according to `Bypass` or `FailClosed`; PostgreSQL remains durable and
  periodic refresh repairs convergence.
- **Scale-down and SIGTERM:** readiness SHOULD be withdrawn first. The shutdown
  grace period MUST allow `Close` to cancel and drain watcher, reconnect,
  debounce, and refresh goroutines. The caller's shutdown context bounds that
  wait.
- **Abrupt loss:** in-memory and optionally cached state may be lost. A durable
  PostgreSQL acknowledgement remains committed and MUST be handled as such by
  the caller.

## Policy composition and observability

`Runtime` owns snapshot validation, single-flight, lifecycle, and monotonic
replacement. It intentionally does not implement retry, circuit-breaker,
bulkhead, adaptive-concurrency, or shared-budget algorithms. Applications
SHOULD compose their existing policies once through `RefreshExecutor`; nested
independent retry or timeout loops are NOT RECOMMENDED because they multiply
attempts and violate shared budgets. Valkey remains a provider adapter, not a
second resilience-policy stack.

Applications SHOULD record bounded, value-free metrics for snapshot age and
revision changes, readiness, refresh latency and result, joined refreshes,
committed-write reconciliation failures, invalidation drops, watcher reconnects,
and convergence latency. Setting values, owner identifiers, credentials, and
cache documents MUST NOT appear in logs, traces, metric labels, fixtures, or
mutation artifacts.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

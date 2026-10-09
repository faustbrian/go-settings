# API reference

The published signature reference is
`go doc github.com/faustbrian/go-settings/v2` for released v2.0.0.
The current source prepares unpublished `github.com/faustbrian/go-settings/v3`;
use `go doc .` and the local subpackages while reviewing that candidate.
This page groups its contracts. The PostgreSQL `DB` seam returns pgx 5.11
rows, so custom implementations must provide `TypeMap() *pgtype.Map`.
See [migration guidance](migrations.md#postgresql-interface-upgrade).

`Codec[T]` supplies a stable ID and version plus typed encoding and decoding.
`Key[T]` adds namespace, stable name, display name, documentation, validation,
an optional default, and sensitivity. `Registry` rejects duplicate, invalid,
or codec-incompatible definitions. Built-ins cover booleans, integers, exact
decimals, strings, durations, times, typed enums, string lists, and JSON.

`Global`, `Tenant`, `User`, and `Resource` create owner scopes. `Chain` declares
precedence. `Resolve` returns a typed value, status, owner, version, and path.
`Capture` and `ResolveSnapshot` provide immutable reads.

`Runtime` validates and atomically serves one process-local last-known-good
snapshot. `NewRuntime` requires a snapshot-capable provider, explicit bounded
policies for every `SettingClass`, and a bounded refresh. `Start`, `Ready`,
`Refresh`, `ResolveCurrent`, `Apply`, and `Shutdown` expose lifecycle,
freshness, same-pod read-after-write, and shutdown behavior. `Shutdown` is
repeatable and concurrency-safe; every caller waits for the same complete
owned-goroutine drain within its own context. Once shutdown begins, cancellation
ends only that caller's wait and does not stop the drain. If a caller's context
ends while `Start` is still in progress, shutdown has not begun and a later
call is required. Calls made after the drain return nil even with a canceled
context. The deprecated `Close(ctx)` method delegates to `Shutdown(ctx)`.
`SnapshotStore` supplies an
optional caller-encrypted cold-start cache; `InvalidationSource` supplies
data-free convergence hints. See [fleet resilience](fleet-resilience.md).

`Set`, `Clear`, and `Inherit` are distinct. Compare-and-set variants fence
concurrent changes. `PrepareSet` creates typed heterogeneous mutations for
`Bulk`. Every write requires actor and reason metadata.

`Provider` exposes exact capabilities plus reads, writes, bulk operations, and
history. `Export` and `Import` use a versioned schema-aware document.

Packages: `memory` is deterministic; `postgres` is durable; `valkey` caches and
invalidates; `migration` evolves definitions; `audit` reads history safely;
and `settingstest` supplies third-party provider conformance.

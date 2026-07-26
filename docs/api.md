# API reference

The canonical signature reference is `go doc github.com/faustbrian/golib/pkg/settings`
and `go doc` for each subpackage. This page groups the contracts.

`Codec[T]` supplies a stable ID and version plus typed encoding and decoding.
`Key[T]` adds namespace, stable name, display name, documentation, validation,
an optional default, and sensitivity. `Registry` rejects duplicate, invalid,
or codec-incompatible definitions. Built-ins cover booleans, integers, exact
decimals, strings, durations, times, typed enums, string lists, and JSON.

`Global`, `Tenant`, `User`, and `Resource` create owner scopes. `Chain` declares
precedence. `Resolve` returns a typed value, status, owner, version, and path.
`Capture` and `ResolveSnapshot` provide immutable reads.

`Set`, `Clear`, and `Inherit` are distinct. Compare-and-set variants fence
concurrent changes. `PrepareSet` creates typed heterogeneous mutations for
`Bulk`. Every write requires actor and reason metadata.

`Provider` exposes exact capabilities plus reads, writes, bulk operations, and
history. `Export` and `Import` use a versioned schema-aware document.

Packages: `memory` is deterministic; `postgres` is durable; `valkey` caches and
invalidates; `migration` evolves definitions; `audit` reads history safely;
and `settingstest` supplies third-party provider conformance.

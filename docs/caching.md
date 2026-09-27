# Caching semantics

`BoundedStale` reads Valkey first and bounds staleness by TTL plus invalidation
delivery. Pub/Sub is at-most-once and messages may be lost, delayed, duplicated,
or reordered. TTL repairs missed events. `Strong` always reads durable storage.

`Config.Prefix` is required and must be unique to the application deployment
and environment that owns the durable settings data. Construction remains
source-compatible, but every provider operation fails closed when the prefix
is empty, longer than 255 bytes, or contains Unicode control characters. There
is no shared default namespace.

`Bypass` treats cache outages as misses; `FailClosed` returns them. Writes commit
durably first, atomically store the value or tombstone only when its version is
newer, then publish a versioned data-free hint. `CacheError` with
`Committed=true` means only cache work failed. `BulkGet` always uses the
durable snapshot operation. Watches are bounded, cancellable, and coalesce the
oldest queued event when full; consumers must reconcile durable state. Runtime
periodic refresh repairs a lost hint. Messages larger than 4 KiB are rejected
before JSON decoding. Runtime stale-event decisions use only its immutable
last-known-good snapshot and retain no attacker-controlled event identities;
future, unknown, or malformed hints trigger debounced durable reconciliation.
See [fleet resilience](fleet-resilience.md).

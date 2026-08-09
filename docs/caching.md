# Caching semantics

`BoundedStale` reads Valkey first and bounds staleness by TTL plus invalidation
delivery. Pub/Sub is at-most-once and messages may be lost, delayed, duplicated,
or reordered. TTL repairs missed events. `Strong` always reads durable storage.

`Bypass` treats cache outages as misses; `FailClosed` returns them. Writes commit
durably first, atomically store the value or tombstone only when its version is
newer, then publish a versioned data-free hint. `CacheError` with
`Committed=true` means only cache work failed. `BulkGet` always uses the
durable snapshot operation. Watches are bounded, cancellable, and coalesce the
oldest queued event when full; consumers must reconcile durable state. Runtime
periodic refresh repairs a lost hint; see [fleet resilience](fleet-resilience.md).

# Caching semantics

`BoundedStale` reads Valkey first and bounds staleness by TTL plus invalidation
delivery. Pub/Sub is at-most-once and messages may be lost, delayed, duplicated,
or reordered. TTL repairs missed events. `Strong` always reads durable storage.

`Bypass` treats cache outages as misses; `FailClosed` returns them. Writes commit
durably first, refresh or delete cache, then publish. `CacheError` with
`Committed=true` means only cache work failed. `BulkGet` always uses the
durable snapshot operation. Watches are bounded, cancellable, and coalesce the
oldest queued event when full; consumers must reconcile durable state.

# Caching semantics

[Documentation index](README.md) · [Repository entry](../README.md)

`BoundedStale` reads validated Valkey entries first until transport TTL expiry.
This is not a cluster freshness SLA: a delayed fill can cache an older durable
read, and `Cache.Watch` supplies hints rather than automatically evicting entries.
Pub/Sub is lossy and messages may be lost, delayed, duplicated or reordered.
Consumers reconcile durable state; `Strong` always reads durable storage.

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

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.

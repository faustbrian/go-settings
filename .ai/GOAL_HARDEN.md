# Goal: Harden settings for Production

## Objective

Prove that `settings` remains correct under inheritance, concurrent changes,
provider failures, schema evolution, hostile stored data, and cache
invalidation races.

## Resolution Correctness

- Exhaustively test every scope and precedence combination.
- Prove the distinction between missing, inherited, defaulted, cleared, and
  explicitly stored zero values.
- Verify provenance for every successful and failed resolution.
- Test snapshots across concurrent writes and definition changes.
- Reject cycles, duplicate definitions, incompatible codecs, and ambiguous
  resolution chains.
- Property-test that adding an unrelated scope or key cannot change another
  effective value.

## Write And Concurrency Safety

- Verify compare-and-set, conflict reporting, idempotent retries, and atomic
  bulk-write claims against each provider.
- Race-test registries, caches, subscriptions, snapshots, and concurrent
  readers and writers.
- Test process crashes before and after durable commit, audit append, cache
  invalidation, and event publication.
- Ensure subscribers are bounded and cannot deadlock writes or leak goroutines.
- Document and test duplicate, delayed, reordered, and lost invalidations.

## Persistence And Migration

- Run provider conformance against supported PostgreSQL and Valkey versions.
- Verify malformed rows, unknown codec versions, partial migrations, renamed
  keys, changed defaults, and rollback/restart behavior.
- Prove migration steps are resumable and idempotent.
- Verify old audit records remain readable after schema evolution.
- Test transaction isolation and connection-loss behavior explicitly.

## Security And Privacy

- Fuzz every external and persisted decoder.
- Enforce limits for key size, nesting, collection size, bulk operations,
  history queries, and watcher fan-out.
- Ensure secrets and sensitive settings never appear in errors, logs, traces,
  metrics labels, diffs, or default audit output.
- Test tenant and owner isolation at every provider boundary.
- Review cache keys and invalidation channels for cross-tenant collisions.

## Verification Gates

- Meaningful 100% statement coverage with branch-sensitive assertions.
- Passing race, fuzz, property, integration, and mutation suites.
- Provider failure-injection tests for timeouts, disconnects, stale replicas,
  and partial availability.
- Stable benchmark baselines for resolution, reads, writes, snapshots, and
  invalidation under contention.
- Static analysis, vulnerability scanning, license checks, and dependency
  review with no unexplained failures.

## Release Blockers

Release MUST be blocked by ambiguous precedence, unversioned concurrent writes,
unbounded subscriptions, silent cache inconsistency, tenant leakage, secret
exposure, non-resumable migrations, provider conformance gaps, race findings,
or meaningful coverage below 100%.

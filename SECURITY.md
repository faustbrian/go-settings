# Security policy and threat model

Threat model version: 1

Report vulnerabilities through private repository security advisories. The
threat model includes tenant collisions, hostile stored bytes, invalidation
loss, write races, unbounded inputs, audit leakage, and migration replay.
Applications remain responsible for authorization, key management, network
security, database access control, and backup encryption.

The default branch prepares an unpublished, release-eligible v2 module for the
breaking controls described below. Production and owned consumers must remain
on released v1 until v2 publication and migration gates pass. Do not use a
local `replace` directive to consume this checkout as v1.

## Assets and trust boundaries

Protected assets are setting values, secret-bearing values, owner scope,
versions, audit history, actor and reason metadata, cached snapshots, and
availability of the durable provider. Provider records, import documents,
snapshot files, Valkey records and Pub/Sub messages cross untrusted parsing or
storage boundaries. Scope, key, value, history-query, migration, and runtime
configuration inputs may originate outside this module.

The library validates scope and definition identities, codec metadata, sizes,
versions, state transitions, cache records, snapshots, and invalidations before
using them. Runtime mutations are matched against the registered definition
and configured resolution chain. Direct `Provider` calls intentionally bypass
that registry and therefore require application authorization and correct
first-write sensitivity classification before invocation.

## Threats and controls

- Tenant and owner confusion is constrained by validated scopes and
  scope-and-key persistence identities. Applications authorize the caller for
  that scope before calling this library.
- Injection and parser abuse are constrained by fixed SQL, bounded identifiers,
  values, batches, history reads, cache records, snapshot envelopes, import
  documents, and invalidation messages. Codecs remain responsible for their
  own documented grammar.
- Replay, duplication, reordering, and races are constrained by monotonic
  versions, compare-and-set, serializable writes, atomic bulk operations, and
  last-known-good snapshot replacement. Invalidations are reconciliation hints,
  never authoritative writes.
- Cache collisions are constrained by a required deployment-unique namespace.
  Cache and invalidation availability is bounded by TTL, buffers, debounce,
  refresh timeouts, and caller cancellation.
- Audit disclosure is constrained by definition-aware redaction at the audit
  boundary and monotonic sensitivity in owned providers. Secret values remain
  plaintext at the raw provider and cache interfaces unless callers use an
  encryption codec and protect database, Valkey, backups, and transport.
- Lifecycle failures are constrained by bounded contexts and complete runtime
  shutdown. Callers retain ownership of database pools, Valkey clients,
  transports, encryption keys, snapshot stores, and invalidation sources.

## Cache and invalidation boundaries

Each Valkey cache must be configured with a deployment-unique `Prefix`. An
empty namespace fails closed so deployments sharing one Valkey cluster cannot
reuse each other's cached records or invalidation channel. Invalidation
messages larger than 4 KiB are rejected before decoding. Stale-event decisions
use the immutable last-known-good snapshot instead of untrusted event versions.
Invalidations retain no attacker-controlled identity state;
unknown, malformed, or implausibly future events still trigger bounded durable
reconciliation.

## Sensitivity boundary

Sensitivity is monotonic at provider coordinates. After the memory or
PostgreSQL provider observes `Sensitive=true`, a later direct mutation cannot
downgrade that coordinate or expose its values in new audit records. Runtime
writes additionally require an exact match with the registered definition.

Direct provider callers remain responsible for classifying the first mutation
correctly because providers do not receive the runtime definition registry.
Previously persisted plaintext audit history is not rewritten by this change;
operators must assess and remove or rotate historical data under their own
retention and incident procedures.

## Residual risks and deployment requirements

Run `postgres.Store.Migrate` before deploying code that reads the sensitivity
marker, drain all v1 writers before the first v2 write, and do not roll back to
an unmodified v1 writer after v2 has persisted sensitivity state. Assign a
unique Valkey prefix per application and environment. Restrict direct provider,
database, and Valkey access; authenticate and encrypt network connections;
encrypt sensitive values before persistence; bound caller contexts; and retain
periodic refresh when missed invalidations must converge. The library does not
provide user authentication, authorization policy, secret key custody, network
access control, or historical-data remediation.

These residual risks are accepted at the application boundary rather than
silently delegated to the library:

| Risk | Owner and rationale | Mitigation and review condition |
| --- | --- | --- |
| Unauthorized direct writes or incorrect first-write classification | Application owner; providers have no caller identity or definition registry | Authorize each scope and classify every first write; review when provider access or definitions change |
| Raw values, actor/reason metadata, keys, or historical audit disclosure | Application security/data owner; storage and existing history are caller-owned | Encrypt values, protect metadata and network/storage access, assess historical retention; review after exposure or retention changes |
| Missed invalidations or hostile reconciliation load | Application operations owner; hints are not durable delivery | Bound contexts, retain periodic refresh, isolate Valkey access; review when convergence or traffic requirements change |
| Mixed v1/v2 writers or unsafe rollback | Deployment owner; v1 does not preserve the sensitivity marker | Migrate before v2 reads, drain v1 writers before v2 writes, use a reviewed backport or stopped-traffic data restoration for rollback; review every upgrade/rollback |

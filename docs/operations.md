# Operations

Monitor durable latency, conflicts, audit transaction failures, cache errors,
subscriber disconnects, and migration checkpoints. Never label metrics with
owners or values. On PostgreSQL outage, operations fail; cached reads are not
durable fallback. On Valkey outage, behavior follows `Bypass` or `FailClosed`.

Retry transient database failures with bounded backoff and versions. Reconcile
`CacheError{Committed:true}` before retry. History limits are 1..1000; bulk,
import, and export cap at 1000 coordinates; values cap at 1 MiB.

For Kubernetes deployments, expose `Runtime.Ready` as readiness, keep liveness
dependency-independent, alert before class-specific maximum staleness, and
drain `Runtime.Close` inside the SIGTERM grace period. Track refresh and
convergence windows without value or owner labels. The complete startup,
failover, rollout, and shutdown contract is in
[fleet resilience](fleet-resilience.md).

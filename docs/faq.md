# FAQ

**Why not environment variables?** They are boot configuration and lack owner
precedence, runtime changes, optimistic versions, and application audit.

**Is clear deletion?** No. Clear blocks fallback; inherit resumes it.

**Is this a feature-flag library?** No rollout, targeting, experiments, or flag
lifecycle are implemented.

**Do snapshots hold database transactions?** No. Capture briefly bulk-reads and
then owns immutable copies.

**Is Pub/Sub loss safe?** TTL bounds it under `BoundedStale`; use `Strong` when
staleness is unacceptable. Always reconcile from durable storage.

**Can it store secrets?** Yes with caller-owned encryption and normal database
controls, but the package does not manage keys.

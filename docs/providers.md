# Providers

`memory.New()` is deterministic, atomic, concurrent, and intended for tests or
single-process local use. It is not durable.

For PostgreSQL, create a `pgxpool.Pool`, call `postgres.New`, and run `Migrate`
through the deployment schema process. Values and audit history commit in one
serializable transaction. Bulk writes are atomic and snapshot reads use a
read-only repeatable-read transaction. PostgreSQL 16 and 17 are supported.
`Migrate` also installs the durable sensitivity marker used to prevent later
direct mutations from weakening audit redaction.

Valkey wraps a durable provider; it is not the source of truth. Construct a
native transport from `valkey-go`, then call `valkey.New`. A deliberate
Valkey-only provider is a separate application-owned tradeoff. Supply a
deployment-unique `valkey.Config.Prefix`; empty or unsafe prefixes make provider
operations fail closed.

Memory and PostgreSQL sensitivity is monotonic per scope and key. Once a
mutation marks a coordinate sensitive, later direct mutations cannot downgrade
new audit records. Direct provider callers must still classify the first write
correctly; runtime callers are checked against registered key definitions.

Third-party providers must advertise exact capabilities and pass
`settingstest.RunProvider`. Never emulate guarantees a backend cannot provide.

Callers retain and close provider connections, PostgreSQL pools, Valkey clients,
transports, snapshot stores, and invalidation sources. `Runtime.Shutdown`
cancels and drains the package-owned timers and goroutines; it does not close
those caller-owned collaborators.

# Providers

`memory.New()` is deterministic, atomic, concurrent, and intended for tests or
single-process local use. It is not durable.

For PostgreSQL, create a `pgxpool.Pool`, call `postgres.New`, and run `Migrate`
through the deployment schema process. Values and audit history commit in one
serializable transaction. Bulk writes are atomic and snapshot reads use a
read-only repeatable-read transaction. PostgreSQL 16 and 17 are supported.

Valkey wraps a durable provider; it is not the source of truth. Construct a
native transport from `valkey-go`, then call `valkey.New`. A deliberate
Valkey-only provider is a separate application-owned tradeoff.

Third-party providers must advertise exact capabilities and pass
`settingstest.RunProvider`. Never emulate guarantees a backend cannot provide.

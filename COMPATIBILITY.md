# Compatibility Policy

Each releasable directory is an independent Go module and follows semantic
versioning. Root-module releases use `v<version>` tags.

The published security upgrade is `github.com/faustbrian/go-settings/v2`
v2.0.0. This source tree prepares the unpublished `/v3` module because pgx
5.11 adds `TypeMap` to the `pgx.Rows` interface exposed by `postgres.DB`.
Existing custom rows implementations need that method; this is a source
incompatibility with published v2, not merely a test-mock version change.
Use published v2 until v3 publication and the documented consumer migration
are complete. Local `replace` directives must not bridge that boundary.

The v3 migration changes imports and the PostgreSQL implementation contract,
not the storage schema. Both majors still use pgx's module: Go MVS can select
pgx 5.11 for a mixed-major application, so `/v3` does not isolate upstream
interface changes. See [migration guidance](docs/migrations.md#postgresql-interface-upgrade).

Version 1 and version 2 writers must not run concurrently. Drain v1 writers
before the first v2 write. Once v2 has persisted sensitivity state, rollback to
an unmodified v1 writer is unsafe because v1 does not preserve that state.

Before `v1`, minor releases MAY contain reviewed breaking changes, but every
break MUST be documented with migration guidance. Patch releases MUST remain
backward compatible. At and after `v1`, incompatible exported API or documented
behavior changes require a new major version.

Compatibility includes exported Go APIs, error classification, serialization,
protocol behavior, persistence schemas, environment variables, command output,
resource ownership, ordering, retry/idempotency semantics, and documented
defaults. A compile-compatible change can still be behaviorally breaking.

Specification-backed modules MUST NOT diverge from their declared standards.
Ambiguities require documented decisions and stable tests. Deprecated APIs
follow [`DEPRECATION.md`](DEPRECATION.md).

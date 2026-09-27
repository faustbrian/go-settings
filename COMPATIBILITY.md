# Compatibility Policy

Each releasable directory is an independent Go module and follows semantic
versioning. Root-module releases use `v<version>` tags.

The published `github.com/faustbrian/go-settings` module is the stable v1
line. This source tree prepares the active, release-eligible
`github.com/faustbrian/go-settings/v2` module because its Valkey defaults and
PostgreSQL read schema intentionally differ from v1. Version 2 is not publicly
available until required main CI passes and `v2.0.0` is published. Existing owned and
external consumers must remain on released v1; local `replace` directives must
not bridge the unpublished boundary.

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

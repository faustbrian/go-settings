# Changelog

## [Unreleased] - root v3.0.0

The current source prepares unpublished `/v3` because pgx 5.11 expands the
`pgx.Rows` interface exposed by `postgres.DB`. Published v2.0.0 remains
available; do not substitute this candidate through a local `replace`.
See the v2-to-v3 migration in `docs/migrations.md` before adopting v3 after
publication. The storage schema is unchanged, and mixed-major applications
still share one pgx version through Go MVS.

### Changed

- Move the root and subpackage imports to `/v3` for the PostgreSQL interface
  incompatibility. Custom `pgx.Rows` implementations must implement its new
  `TypeMap` method; use a compatible mock implementation for `postgres.DB`.
- Use qualified immutable source tooling in CI to keep mandatory analyzers
  compatible with patched Go. The checksum-pinned local release is unchanged.
- Select Go 1.27.2 for CI to include the crypto/tls fix for GO-2026-6607.
  The module minimum remains Go 1.27.0; applications must be rebuilt with
  a patched toolchain to receive the standard-library fix.
- Adopt pgx v5.11 for the PostgreSQL provider. Custom implementations returning
  `pgx.Rows` must implement its new `TypeMap` method; use a compatible mock
  implementation when testing the exported `postgres.DB` seam.

## 2.0.0 - 2026-09-27

Published `/v2` introduces the security hardening below relative to v1.
Complete the documented schema, sensitivity and namespace rollout before
introducing v2 writers; publication does not establish application migration.

### Changed

- Require a deployment-unique Valkey namespace instead of sharing the
  `settings` default, and fail closed on missing or unsafe prefixes.
- Persist sensitivity monotonically in memory and PostgreSQL providers so
  direct callers cannot downgrade redaction after a coordinate is classified.
- Require v1 writers to drain before v2 writes begin; rollback to an unmodified
  v1 writer is unsafe after v2 persists sensitivity state.

### Fixed

- Reject oversized Valkey invalidations before decoding, retain no
  attacker-controlled invalidation identities, and prevent forged future
  versions from suppressing later legitimate reconciliation.
- Redact hostile invalidation tokens from watcher errors while retaining safe
  cache-error classification.

### Security

- Prevent cross-deployment cache collisions, invalidation-driven unbounded
  memory growth, and sensitivity downgrade leakage in new audit records.

## 1.1.0 - 2026-09-09

### Added

- Add repeatable, concurrency-safe `Runtime.Shutdown(ctx)` as the canonical
  complete cancel-and-wait lifecycle operation.

### Changed

- Adopt the checksum-verified `go-library-tools` v1.4.0 CLI and immutable W14
  workflow so local and hosted cohesion and specification checks use the final
  authoritative contract.
- Correct runtime-resource ownership metadata to reflect package-owned runtime
  state alongside caller-owned provider and transport resources.
- Adopt the `go-library-tools` v1.3.0 schema-v2 cohesion contract and local
  `make cohesion` gate without changing settings API or runtime behavior.
- Pin reusable CI to the v1.3.0 workflow and enforce cohesion metadata in the
  repository's required CI contract.
- Replace copied repository verification scripts with the released,
  checksum-pinned `go-library-tools` contract while retaining settings-owned
  mutation evidence, API baselines, fuzz policy, and benchmark coverage.

### Deprecated

- Deprecate `Runtime.Close(ctx)` in favor of `Runtime.Shutdown(ctx)` while
  preserving it as a source-compatible delegation. The old name conflicts with
  the ecosystem lifecycle vocabulary and may be removed only in an authorized
  next major after the documented time, release, and consumer-proof boundary.

### Documentation

- Clarify that callers close external provider resources while `Runtime.Close`
  drains only package-owned background work.
- Link the module to the immutable v1.4.0 Golib ecosystem guidance.
- Publish the module's family, capabilities, ownership, lifecycle, supported
  environments, package selection, and delivery status, and link the README to
  the immutable v1.3.0 ecosystem index and family guidance.

- Remove the archived monorepo documentation link; package guidance remains in
  the repository-owned documentation.

## 1.0.0 - 2026-08-25

### Changed

- Exclude intentional nested modules from root local-proxy archives so local,
  bootstrap, CI, and public module checksums describe the same source
  boundary.

- Track the pinned documentation-tool lockfile so clean CI checkouts install
  the exact validated cspell dependency.

- Reconcile standalone dependency checksums against deterministic current
  module archives so CI, local verification, and release consumers resolve
  identical content.

- Harden standalone documentation validation with deterministic spelling and
  link checks, package-specific documentation gates, and repository-local
  contributor guidance.

### Documentation

- Link the package README to the repository-wide Golib documentation portal.

### Added

- Add a bounded fleet runtime with immutable last-known-good snapshots,
  per-class degradation policies, durable write fencing, invalidation-driven
  convergence, periodic repair, cached cold start, readiness, and graceful
  shutdown semantics.
- Add Kubernetes fleet simulation, hostile snapshot fuzzing, real PostgreSQL
  and Valkey fleet integration, and runtime read and refresh benchmarks.

### Changed

- Publish the module from its standalone `github.com/faustbrian/go-settings` identity while preserving its documented API and behavior.
- Upgrade `golang.org/x/text` to v0.41.0 and `golang.org/x/sys` to v0.47.0 so
  the dependency graph no longer contains GO-2026-5970 or GO-2026-5024.
- Make Valkey cache replacement version-conditional and preserve versioned
  tombstones so delayed fills cannot regress values or resurrect inherited
  settings.
- Bind runtime reads and defaults to registered definition metadata so callers
  cannot weaken a setting class or substitute fallback values.
- Delegate local mutation checks to the canonical exact-100 repository runner
  instead of broad package exclusions and a reduced efficacy threshold.

### Fixed

- Make native Valkey subscription coverage deterministic across CI runners.
- Preserve deterministic newest-event coalescing without unreachable fallback
  branches in the Valkey watcher.

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

- Establish typed runtime settings, memory and PostgreSQL providers, optional
  Valkey caching, audit history, import/export, snapshots, and migrations.

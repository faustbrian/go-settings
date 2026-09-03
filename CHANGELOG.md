# Changelog

## Unreleased

### Changed

- Adopt the `go-library-tools` v1.3.0 schema-v2 cohesion contract and local
  `make cohesion` gate without changing settings API or runtime behavior.
- Pin reusable CI to the v1.3.0 workflow and enforce cohesion metadata in the
  repository's required CI contract.

- Replace copied repository verification scripts with the released,
  checksum-pinned `go-library-tools` contract while retaining settings-owned
  mutation evidence, API baselines, fuzz policy, and benchmark coverage.

### Documentation

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

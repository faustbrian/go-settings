# Changelog

## Unreleased

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

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

- Establish typed runtime settings, memory and PostgreSQL providers, optional
  Valkey caching, audit history, import/export, snapshots, and migrations.

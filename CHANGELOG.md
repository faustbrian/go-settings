# Changelog

## Unreleased

### Changed

- Delegate local mutation checks to the canonical exact-100 repository runner
  instead of broad package exclusions and a reduced efficacy threshold.

### Fixed

- Make native Valkey subscription coverage deterministic across CI runners.

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

- Establish typed runtime settings, memory and PostgreSQL providers, optional
  Valkey caching, audit history, import/export, snapshots, and migrations.

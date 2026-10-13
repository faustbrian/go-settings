# Package map

[Documentation index](README.md) · [Repository entry](../README.md)

This map follows the current source modules, not a synchronized release.
Use the repository README for published installation versions; an unpublished
source major is not installable merely because its package appears here.
Internal packages and non-releasable fixtures are intentionally excluded.

## github.com/faustbrian/go-settings/v3

`settings` is a typed runtime-settings library for values that operators, tenants, users, and resources change while an application is running. It provides explicit precedence, immutable snapshots, optimistic writes, audit history, schema evolution, PostgreSQL persistence, and optional Valkey caching.

| Import path | Responsibility |
| --- | --- |
| [`github.com/faustbrian/go-settings/v3`](../doc.go) | Use for typed runtime settings, precedence, immutable snapshots, optimistic writes, import and export, and explicit runtime convergence. |
| [`github.com/faustbrian/go-settings/v3/audit`](../audit/audit.go) | Use to read setting history with definition-aware redaction. |
| [`github.com/faustbrian/go-settings/v3/memory`](../memory/store.go) | Use for a deterministic concurrent in-memory provider in tests or local applications. |
| [`github.com/faustbrian/go-settings/v3/migration`](../migration/migration.go) | Use to execute resumable setting-definition migrations against a provider and journal. |
| [`github.com/faustbrian/go-settings/v3/postgres`](../postgres/store.go) | Use for durable PostgreSQL settings, history, snapshots, and migration journaling. |
| [`github.com/faustbrian/go-settings/v3/settingstest`](../settingstest/conformance.go) | Use to verify custom providers against the shared settings conformance contract. |
| [`github.com/faustbrian/go-settings/v3/valkey`](../valkey/cache.go) | Use for an optional Valkey cache and invalidation layer in front of durable settings storage. |

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.

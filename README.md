# settings

[![CI](https://github.com/faustbrian/go-settings/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-settings/actions/workflows/ci.yml)
[![CodeQL](https://img.shields.io/badge/CodeQL-required-blue)](https://github.com/faustbrian/go-settings/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Mutation](https://img.shields.io/badge/mutation-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Documentation](https://img.shields.io/badge/docs-checked_in_CI-blue)](docs/)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-settings/v2.svg)](https://pkg.go.dev/github.com/faustbrian/go-settings/v2)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-settings?sort=semver)](https://github.com/faustbrian/go-settings/releases)
[![Go](https://img.shields.io/badge/go-1.27.0-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`settings` is a typed runtime-settings library for values that operators,
tenants, users, and resources change while an application is running. It
provides explicit precedence, immutable snapshots, optimistic writes, audit
history, schema evolution, PostgreSQL persistence, and optional Valkey caching.

Settings are application data. They are not process boot configuration,
feature flags, authorization decisions, a secrets manager, or a business-rule
engine. See [the comparison](docs/comparison.md) before adopting the package.

The published security upgrade is
`github.com/faustbrian/go-settings/v2` v2.0.0, requiring Go 1.27. Source remains
at the repository root on main; Git tags select versions. The original v1
line remains affected by the disclosed cache and audit vulnerabilities.
Public availability does not establish an application's migration: complete
the [schema, sensitivity and namespace rollout](docs/migrations.md#security-hardening-upgrade)
before deploying v2 writers. Do not substitute this checkout for v1 with a
local `replace` directive.

The current source prepares unpublished `/v3` v3.0.0 for pgx 5.11's new
`pgx.Rows.TypeMap` requirement on custom PostgreSQL implementations.
Published v2 remains the install target below until v3 is released. See
the [v2-to-v3 migration](docs/migrations.md#postgresql-interface-upgrade)
for import, mock and mixed-major dependency considerations.

CI uses Go 1.27.2 while the module minimum remains Go 1.27.0. Build
applications with a patched Go release; this CI selection does not patch
the runtime of already-built applications.

## Install

Install the released v2 module and use `/v2` imports:

```sh
go get github.com/faustbrian/go-settings/v2@v2.0.0
```

```go
theme := settings.NewKey("ui", "theme", settings.StringCodec{},
    settings.WithDefault("system"),
)
result, err := settings.Resolve(ctx, provider, theme,
    settings.Chain(settings.User(userID), settings.Tenant(tenantID), settings.Global()),
)
```

The root package has no PostgreSQL or Valkey imports. Applications opt into
backend dependencies by importing `postgres` or `valkey`.

For a long-lived runtime, call `Start(ctx)` with the process lifetime and
`Shutdown(ctx)` during termination. Shutdown is repeatable, safe for concurrent
callers, and waits for package-owned background work within the caller's
context. A call whose context ends while `Start` is still in progress does not
initiate shutdown, so the application must call `Shutdown` again. The
deprecated `Close(ctx)` method remains a compatibility alias; see the
[migration guidance](docs/migrations.md#runtime-lifecycle).

## Documentation

- [Quick start](docs/quick-start.md)
- [API reference](docs/api.md)
- [Scopes and precedence](docs/scopes-and-precedence.md)
- [Provider setup](docs/providers.md)
- [PostgreSQL schema management](docs/schema-management.md)
- [Caching semantics](docs/caching.md)
- [Runtime fleet resilience](docs/fleet-resilience.md)
- [Migration guidance](docs/migrations.md)
- [Secret handling](docs/secrets.md)
- [Operations](docs/operations.md)
- [Adoption guide](docs/adoption.md)
- [FAQ](docs/faq.md)
- [Testing and local commands](docs/testing.md)
- [Benchmark baseline](docs/benchmarks.md)

Requires Go 1.27+, PostgreSQL 16 or 17 for durability, and Valkey 9 when
caching is enabled. Licensed under the [MIT License](LICENSE).

For ecosystem-wide selection and ownership guidance, see the versioned
[Golib ecosystem index](https://github.com/faustbrian/go-library-tools/blob/v1.4.0/docs/ecosystem/README.md)
and its
[Persistence and durability family](https://github.com/faustbrian/go-library-tools/blob/v1.4.0/docs/ecosystem/design-language.md#package-families-and-selection).

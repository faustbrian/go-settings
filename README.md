# settings

[![CI](https://github.com/faustbrian/go-settings/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-settings/actions/workflows/ci.yml)
[![CodeQL](https://img.shields.io/badge/CodeQL-required-blue)](https://github.com/faustbrian/go-settings/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Mutation](https://img.shields.io/badge/mutation-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Documentation](https://img.shields.io/badge/docs-checked_in_CI-blue)](docs/)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-settings.svg)](https://pkg.go.dev/github.com/faustbrian/go-settings)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-settings?sort=semver)](https://github.com/faustbrian/go-settings/releases)
[![Go](https://img.shields.io/badge/go-1.26.6-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`settings` is a typed runtime-settings library for values that operators,
tenants, users, and resources change while an application is running. It
provides explicit precedence, immutable snapshots, optimistic writes, audit
history, schema evolution, PostgreSQL persistence, and optional Valkey caching.

Settings are application data. They are not process boot configuration,
feature flags, authorization decisions, a secrets manager, or a business-rule
engine. See [the comparison](docs/comparison.md) before adopting the package.

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

Requires Go 1.26+, PostgreSQL 16 or 17 for durability, and Valkey 9 when
caching is enabled. Licensed under the [MIT License](LICENSE).

## Ecosystem

Use the [Golib documentation portal](https://github.com/faustbrian/golib/blob/main/docs/index.md)
to choose companion packages, supported stacks, recipes, and operations guidance.

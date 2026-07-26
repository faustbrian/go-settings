# settings

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
- [Migration guidance](docs/migrations.md)
- [Secret handling](docs/secrets.md)
- [Operations](docs/operations.md)
- [Adoption guide](docs/adoption.md)
- [FAQ](docs/faq.md)
- [Testing and local commands](docs/testing.md)

Requires Go 1.26+, PostgreSQL 16 or 17 for durability, and Valkey 9 when
caching is enabled. Licensed under the [MIT License](LICENSE).

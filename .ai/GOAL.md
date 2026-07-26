# Goal: Production-Grade Runtime Settings for Go

## Objective

Build `settings` as an open-source, typed runtime-settings library for
persisted values that operators, tenants, users, or resources may change while
an application is running.

The package MUST remain distinct from `config`. Configuration describes how
a process boots and connects to infrastructure. Settings are application data
with ownership, precedence, persistence, audit history, and runtime changes.

## Core Principles

- Public APIs MUST be explicit, typed, deterministic, and safe for concurrent
  use.
- Missing, inherited, defaulted, invalid, and explicitly cleared values MUST be
  distinguishable.
- Resolution precedence MUST be declared rather than hidden in global state.
- Reads MUST support immutable snapshots for request and job consistency.
- Writes MUST support optimistic concurrency and auditable change metadata.
- Secrets MAY be stored through an explicit encryption codec, but this package
  MUST NOT own encryption keys or become a secrets manager.
- The package MUST NOT implement feature flags, authorization, boot
  configuration, or arbitrary business rules.

## Required Model

The public model MUST include:

- typed setting keys with codecs, validation, documentation, and defaults;
- namespaces and registries that reject duplicate or incompatible definitions;
- owner scopes such as global, tenant, user, and resource;
- caller-defined resolution chains and precedence;
- value provenance showing the effective owner, version, and fallback path;
- explicit set, clear, inherit, compare-and-set, and bulk operations;
- immutable snapshots with stable versions;
- change records with actor, reason, timestamp, and redacted values;
- import and export formats with schema/version metadata; and
- migrations for key renames, value transformations, and default changes.

The API MUST support booleans, integers, decimals, strings, durations, times,
enums, string lists, structured values, and caller-defined codecs without
reducing all consumer code to `any` or unchecked type assertions.

## Providers And Caching

Provide a small provider contract and first-party implementations for:

- deterministic in-memory storage for tests and local use;
- PostgreSQL as the durable production provider; and
- Valkey as an optional cache and invalidation transport, not the sole durable
  source unless a caller explicitly accepts that tradeoff.

Providers MUST expose capabilities rather than silently emulating unsupported
transactions, subscriptions, history, or compare-and-set behavior.

Caching MUST define consistency, invalidation, stale-read, outage, and
read-after-write behavior. Watchers and subscriptions MUST be bounded,
cancellable, and explicit about coalescing and delivery guarantees.

## Persistence And Evolution

- Schema ownership and migration instructions MUST be documented.
- Concurrent writes MUST use versions or compare-and-set semantics.
- Multi-setting updates MUST state whether they are atomic.
- Setting definitions MUST have stable identifiers independent of display
  names.
- Migration execution MUST be resumable and idempotent.
- Historical records MUST remain interpretable after codec or definition
  changes.
- Sensitive values MUST be redacted from errors, logs, traces, and audit output.

## Package Structure

Prefer focused packages such as:

- `settings` for keys, values, scopes, snapshots, and resolution;
- `memory` for the in-memory provider;
- `postgres` for durable storage;
- `valkey` for cache and invalidation integration;
- `migration` for definition evolution;
- `audit` for change-history contracts; and
- `settingstest` for reusable provider conformance tests.

Backend dependencies MUST remain additive. Importing the root package MUST NOT
pull PostgreSQL or Valkey clients into applications that do not use them.

## Testing And Quality

- Meaningful 100% statement coverage is REQUIRED; assertions MUST validate
  behavior and failure contracts rather than merely execute lines.
- Every provider MUST pass the same conformance suite.
- Property tests MUST cover precedence, fallback, snapshots, and migrations.
- Fuzz tests MUST cover codecs, imports, malformed persisted data, and scope
  identifiers.
- Race tests MUST cover registries, concurrent reads/writes, caches, and
  watchers.
- Integration tests MUST use real PostgreSQL and Valkey instances.
- Mutation testing MUST demonstrate that important assertions detect changed
  resolution and concurrency behavior.
- Benchmarks MUST measure hot reads, cold reads, resolution depth, bulk reads,
  cache invalidation, and provider contention.

## Documentation And Delivery

Documentation MUST include a complete API reference, quick start, typed-key
examples, scope and precedence recipes, provider setup, schema management,
caching semantics, migration guidance, secret-handling caveats, operational
guidance, adoption guide, FAQ, and comparison with config and feature flags.

CI MUST enforce formatting, vetting, strict linting, tests, race tests, fuzz
smoke tests, coverage, vulnerability scanning, dependency review, examples,
documentation links, and reproducible benchmarks. All checks MUST be runnable
locally through documented commands.

## Completion Criteria

The package is complete only when the public contracts, providers, conformance
suite, migrations, security limits, documentation, examples, CI, benchmarks,
and meaningful 100% coverage are implemented and verified. A memory-only map
or untyped key/value wrapper does not satisfy this goal.

# Testing and local commands

`make check` runs format and module checks, vet, tests, race, exact coverage,
fuzz smoke tests, strict linting, static analysis, vulnerability scanning,
documentation, examples, benchmarks, and workflow linting.

Real providers require `POSTGRES_URL` and `VALKEY_ADDR`; run `make integration`.
Use `make mutation` for the canonical repository mutation gate and `make
benchmark` for reproducible runs. Mutation requires exact 100% efficacy and
mutant coverage for every viable mutant. CI covers
PostgreSQL 16/17 and Valkey 9. The integration target also runs a two-runtime
fleet through real PostgreSQL durability and Valkey invalidation. Fuzz targets
cover codecs, imports, stored bytes, scopes, and cached snapshot envelopes;
property tests cover precedence and isolation; race tests cover registries,
providers, snapshots, refreshes, and watchers. Kubernetes simulations exercise
multi-replica invalidation delivery, loss, reordering, periodic repair, and
graceful drain without requiring a cluster.

# Testing and local commands

`make check` runs format and module checks, vet, tests, race, exact coverage,
fuzz smoke tests, strict linting, static analysis, vulnerability scanning,
documentation, examples, benchmarks, and workflow linting.

Real providers require `POSTGRES_URL` and `VALKEY_ADDR`; run `make integration`.
Use `make mutation` for the canonical repository mutation gate and `make
benchmark` for reproducible runs. Mutation requires exact 100% efficacy and
mutant coverage for every viable mutant. CI covers
PostgreSQL 16/17 and Valkey 9. Fuzz targets cover codecs, imports, stored bytes,
and scopes; property tests cover precedence and isolation; race tests cover
registries, providers, snapshots, and watchers.

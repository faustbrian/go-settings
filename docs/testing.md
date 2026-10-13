# Testing and local commands

[Documentation index](README.md) · [Repository entry](../README.md)

`make check` and `make ci` use the public tool pinned in `.golib.yaml` for
bounded checks: format/module validation, deterministic tests/seeds, vet,
configured static/security checks, documentation links, API compatibility and
the targeted root race. Base-free checks do not actively fuzz; changed-source
CI selects causal active smoke. `make ci` also validates repository/cohesion
structure and offline specification authority.

Real providers require `POSTGRES_URL` and `VALKEY_ADDR`; the shared tool supplies
the declared PostgreSQL/Valkey fixtures without a test tag. Direct unconfigured
Go tests skip those providers, so they do not establish backend qualification.
Use `make check-full` for the original full contract, including coverage,
mutation, the five 2s/2m fuzz operations and the 100x root/memory/Valkey
benchmark operation. Optional `golib mutation --module . --package .` selects
one package, one worker and a twenty-minute deadline. Full mutation requires
exact 100% efficacy and mutant coverage for every viable mutant.
Real integration tests also run a two-runtime fleet through PostgreSQL
durability and Valkey invalidation. Fuzz targets
cover codecs, imports, stored bytes, scopes, and cached snapshot envelopes;
property tests cover precedence and isolation; race tests cover registries,
providers, snapshots, refreshes, and watchers. Kubernetes simulations exercise
multi-replica invalidation delivery, loss, reordering, periodic repair, and
graceful drain without requiring a cluster.

The registry workload stresses synchronization without asserting business
outcomes; the snapshot characterization uses sequential writes and verifies
captured value/version stability. The selected lifecycle tests assert startup
joining and permanent closure. Do not present the first two as a concurrent
snapshot-coherence proof.

Scheduled CI uses the shared bounded route, not an automatic full campaign.
`make specification-check-online` and `golib release dry-run --module .` select
online authority and full release rehearsal explicitly. See
[contributor verification](../CONTRIBUTING.md#verification) for standalone
tests, installation and disposable-cache sessions.

Choose the next guide in the [documentation index](README.md)
or return to the [repository entry](../README.md) for installation and ownership.

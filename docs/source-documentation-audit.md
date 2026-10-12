# Source documentation audit

## Acceptance and scope

Documentation-only pass from main `b9c15d48807e587fda64cc058d9b889787a38a65`.
The owner is this standalone settings repository; direct consumers use typed
keys, provider adapters, captured snapshots and the process-local Runtime.
Comments must explain observable contracts without changing executable source,
SQL/Lua, fixture bytes, API declarations, dependencies or historical authority.

Reproduce the inventory with `git ls-files '*.go' '*go.mod'` and
`golib source-docs inventory --json`. The tracked scope is one `/v3` module,
65 Go files (19 production and 46 test files), and seven source packages:
settings, audit, memory, migration, postgres, settingstest and valkey.
There are no tracked generated Go files, vendored Go files or build directives.
PostgreSQL SQL and Valkey Lua are executable inputs, not prose to rewrite.

The initial objective inventory reports 315 exported member/declaration comment
gaps. Existing package entries exist but omit boundaries needed for safe use.
Five fuzz targets, eight benchmarks and two runnable examples are included;
service-conditioned provider/fleet tests are not service qualification when
those services are absent. Test namespaces are not separate public packages.

## Missing documentation versus investigated contracts

| Slice | Documentation gap and source-derived contract |
| --- | --- |
| Definitions, codecs and registry | Missing methods/fields and borrowed default/callback assumptions. Registry locks its maps, not caller-owned definitions/codecs. Defaults can alias mutable values; constructor options execute synchronously and are not panic-contained. StringCodec does not validate UTF-8; JSONCodec delegates custom marshaling and does not promise protocol canonicalization. |
| Scope, writes and resolution | Explicit ordered scopes are identifiers, not authorization. Clear blocks fallback; inherit retains provider version tombstones. Direct Resolve performs sequential reads, not a coherent multi-owner transaction. Provider errors and invalid values stop resolution with partial path. CAS pointers are borrowed and audit metadata must not contain values. |
| Providers and audit | Memory serializes transactions but retains unbounded in-process history and calls its clock while holding the write lock. PostgreSQL uses repeatable-read capture and serializable value/history writes without retrying serialization errors. Caller owns connections/migration. Sticky sensitivity protects future history, not retrospective erasure or encryption. History ordering differs across providers. |
| Capture and persistence | Snapshot freezes encoded record buffers, not arbitrary decoded/default T values. Bulk coherence is a provider obligation. Revision is an unkeyed content identifier, not authentication; timestamps/provenance are not hashed. Restore validates against current definitions and accepts only supported origins; caller owns authenticated encrypted storage. |
| Runtime | Copies containers/default encoded bytes but borrows providers, definitions, clock/executor and callbacks. Refresh publishes memory before cache Save, so a persistence error may follow activation. Shared refresh flight is owned by its first caller context. Shutdown joins owned startup/background work, not external manual Apply/Refresh or connection lifetimes; callbacks and IO must cooperate. |
| Valkey | Prefix isolates deployment naming, not access control. TTL/default policy and invalid cache fallback are explicit; atomic snapshots always use durable bulk reads. Durable commit precedes cache/publish and can succeed with CacheError. Pub/sub is lossy, not durable delivery. Caller owns transport/client; decimal version comparison avoids Lua floating-point loss. |
| Migration and portable docs | Migration writes and journal marks are separate; partial Report survives errors. Renames require atomic bulk, transforms use target codec as completion marker, default-change is checkpoint-only. Export relies on provider bulk coherence; Import validates entries against registry, ignores descriptors/source versions and uses new unconditional writes. Neither format provides authentication. |
| Tests and helpers | Existing regression names identify contracts; add only missing input/invariant and measured-operation comments. Conformance is a selected six-case suite, not complete provider qualification. No broad unchanged race, fuzz, benchmark, database or fleet campaign is needed for prose. |

## Native verification and retained boundaries

`.golib.yaml` selects `apidiff` at `api/v3.txt`, not a full-prose snapshot.
Historical `api/baseline.txt` and `api/v2.txt` remain unchanged. There is no
source generation recipe or generated Go exclusion to repair. The PostgreSQL
schema embed directive and all executable templates remain exact.

Applicable proof: format, all-tracked AST/tokens/literals/directives equality,
all seven full package renders, the two native examples, objective source
checks, local documentation/specification checks and changed-content pinned
secret scanning; then one ordinary complete-diff review. The native `check`
route launches PostgreSQL/Valkey and is not a prose-only prerequisite.
Enforcement policy/tool metadata, publication, remote-main CI and release
qualification belong to the coordinator, not this source pass.

No standards-conformance or new performance claim is introduced. Unknown design
rationale is not supplied as fact. Resource bounds are stated only where the
implementation enforces them; cancellation cannot bound noncooperative custom
code, lock acquisition or caller-owned IO. Sensitive flags do not sanitize
arbitrary actor/reason strings, callback errors or prior history.

## Local pass result

Objective source checking reports no remaining structural findings. All 65 Go
files retain the same runtime AST, significant tokens, literals and directives;
one implicit method terminator is added solely to attach RuntimeClock.Now's
comment. Go 1.27.2 rendered all seven packages and the two unchanged examples
passed. The final affected root/migration render was refreshed after prose
review; unaffected renders and example proof were reused, not represented as
new executions. The audit covers all tracked tests and production entries;
fuzz/benchmark notes describe their actual assertion and measurement limits.

Current misleading UTF-8/canonical-JSON, immutable-default, cache freshness,
startup snapshot and audit-renderability assumptions are narrowed to the actual
source. No unresolved rationale is filled with a speculative guarantee. Native
apidiff baselines, module inputs, SQL/Lua and historical artifacts are untouched.
This local documentation result does not qualify database behavior, fleet
convergence, remote CI, release readiness or application migration.

## Objective Enforcement

The checksum-pinned public Tools v2.4.0 verifier now supplies
`make source-docs-check` as a prerequisite of the existing local and CI Make
routes. Ordinary shared CI selects strict source-documentation checking and
requires its result. Native prerequisites, operation budgets, security and
release controls are retained. Hosted exact-head evidence is recorded by the
coordination ledger; configuration alone does not establish green delivery.

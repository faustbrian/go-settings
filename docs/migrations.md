# Migrations

A `Plan` has stable source/target schema IDs and ordered rename, transform, or
default-change steps. Execute it for explicit scopes with actor and reason.
Renames require atomic bulk writes. Transforms use the target codec contract as
an idempotency marker. Default changes do not rewrite inherited owners.

## Runtime lifecycle

Replace `Runtime.Close(ctx)` calls with `Runtime.Shutdown(ctx)` without changing
the call order or context budget. `Close(ctx)` remains source-compatible and
delegates to `Shutdown(ctx)`, but it is deprecated because context-aware
complete shutdown uses the ecosystem `Shutdown(ctx)` vocabulary; `Close` is
reserved for immediate synchronous `io.Closer`-style release.

Both methods wait for an in-progress `Start` before initiating shutdown. If the
shutdown caller's context ends first, the call returns without initiating
shutdown and the application must call `Shutdown` again. After shutdown begins,
a caller context bounds only that caller's wait; cancellation and the owned
drain continue. Concurrent calls join the same drain, and calls made after the
drain return nil even with a canceled context.

Removal may occur only in an authorized next major release after `Shutdown` is
publicly consumable for the longer of 180 days and two stable minor releases
that retain `Close(ctx)`, and only after owned-consumer and clean-consumer
migration proof succeeds.

`Run` consults a `Journal` and checkpoints each step. Steps also recognize their
durable completed state, making a crash between value commit and checkpoint
safe to resume. PostgreSQL implements the journal. Keep old codecs available
where historical audit bytes must remain decodable.

## PostgreSQL interface upgrade

The current source prepares v3.0.0; it is not yet a published install target.
Published v2.0.0 exposes pgx 5.10's `Rows` interface through `postgres.DB`.
pgx 5.11 adds `TypeMap() *pgtype.Map`, so custom rows implementations that
compiled against v2 need that method. The owned major change makes this
source incompatibility explicit.

After v3.0.0 is published:

1. Select `github.com/faustbrian/go-settings/v3@v3.0.0` and change root and
   subpackage imports from `/v2` to `/v3` together. Do not mix settings types
   from the two module paths or substitute a local `replace` directive.
2. Implement `TypeMap() *pgtype.Map` on custom `pgx.Rows` values, returning
   the map used to decode their values. Use compatible `pgxmock/v5` for mock
   implementations; real `pgxpool.Pool` already supplies the new contract.
3. Rebuild with a patched Go toolchain. The module minimum remains 1.27.0;
   development and CI select 1.27.2.

Go MVS selects one pgx version for an application, including applications
that import both settings majors. Adopting v3 can therefore require updating
custom rows used elsewhere in that application, including through v2.
The new owned module path is not upstream dependency isolation.

This upgrade introduces no SQL or storage-schema change. The existing
v1-to-v2 security prerequisites below still apply to applications that have
not completed them; v3 does not make an unmodified v1 writer safe.

## Security hardening upgrade

These steps apply to the v2 upgrade, not to released v1. Do not
point a v1 import at this checkout with a local `replace` directive.

Version 2.0.0 is published and clean public consumer checks have passed.
Publication and deployment are separate stages. The following deployment
work remains application-owned:

1. Assign every application deployment and environment a unique
   `valkey.Config.Prefix`; v2 no longer accepts the v1 empty-prefix fallback.
2. Run `postgres.Store.Migrate` before deploying v2 readers or writers so
   `settings_values.sensitive` exists. A v2 binary must not read a v1 schema.
3. Classify pre-existing sensitive coordinates as described below and drain
   v1 writers before allowing the first v2 write.
4. Move consumers deliberately to published `/v2` only after their schema and
   prefix rollout is complete. Permanent or local `replace` directives are
   not migration evidence.

The schema migration defaults existing coordinates to non-sensitive because
their classification cannot be reconstructed safely. Direct-provider callers
must correctly classify each pre-existing sensitive coordinate on its first v2
mutation, or populate its marker in an application-owned reviewed migration
before allowing direct writes. Schema migration alone cannot recover v1
sensitivity classifications. Assess existing audit
retention separately where secret values may have been written before this
upgrade. Drain every v1 writer before the first v2 write. After v2 records a
sensitive coordinate, v1 writers and ordinary rollback are unsafe because v1
does not preserve the sensitivity marker and can expose plaintext audit data.
Rollback then requires either restoring pre-v2 data under stopped traffic or a
reviewed v1 backport that enforces the marker before writes resume.

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

## Security hardening upgrade

These steps apply to the v2 upgrade, not to released v1. Do not
point a v1 import at this checkout with a local `replace` directive.

Publication and deployment are separate stages:

1. Deliver the reviewed source to main, pass required exact-source CI, and
   publish `github.com/faustbrian/go-settings/v2` with a `v2.0.0` tag.
   Then verify actual clean public consumers resolve the release; local
   source rehearsals do not establish public availability.
2. Assign every application deployment and environment a unique
   `valkey.Config.Prefix`; v2 no longer accepts the v1 empty-prefix fallback.
3. Run `postgres.Store.Migrate` before deploying v2 readers or writers so
   `settings_values.sensitive` exists. A v2 binary must not read a v1 schema.
4. Prove each owned consumer resolves released v1 until publication, then move
   consumers deliberately to `/v2` only after their schema and prefix rollout
   is complete. Permanent or local `replace` directives are not migration
   evidence.

The schema migration defaults existing coordinates to non-sensitive because
their classification cannot be reconstructed safely. Assess existing audit
retention separately where secret values may have been written before this
upgrade. Drain every v1 writer before the first v2 write. After v2 records a
sensitive coordinate, v1 writers and ordinary rollback are unsafe because v1
does not preserve the sensitivity marker and can expose plaintext audit data.
Rollback then requires either restoring pre-v2 data under stopped traffic or a
reviewed v1 backport that enforces the marker before writes resume.

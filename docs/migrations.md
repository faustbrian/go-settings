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

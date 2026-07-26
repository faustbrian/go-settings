# Migrations

A `Plan` has stable source/target schema IDs and ordered rename, transform, or
default-change steps. Execute it for explicit scopes with actor and reason.
Renames require atomic bulk writes. Transforms use the target codec contract as
an idempotency marker. Default changes do not rewrite inherited owners.

`Run` consults a `Journal` and checkpoints each step. Steps also recognize their
durable completed state, making a crash between value commit and checkpoint
safe to resume. PostgreSQL implements the journal. Keep old codecs available
where historical audit bytes must remain decodable.

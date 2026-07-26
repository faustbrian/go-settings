# Schema management

The `postgres` package owns `settings_values`, `settings_history`,
`settings_migrations`, and their indexes. `Schema` exposes idempotent SQL and
`Store.Migrate` executes it. Run it in one controlled deployment step.

Missing tombstones preserve monotonic versions after `Inherit`. Audit rows
retain codec IDs and versions. Migration checkpoints use plan, step, and owner.
Back up values and history together, never edit versions manually, and apply
future schema changes before deploying code that needs them.

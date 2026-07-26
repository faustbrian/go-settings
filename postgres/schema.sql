CREATE TABLE IF NOT EXISTS settings_values (
    scope_kind text NOT NULL,
    scope_id text NOT NULL,
    key_id text NOT NULL,
    state smallint NOT NULL,
    value bytea,
    codec_id text NOT NULL,
    codec_version integer NOT NULL,
    version bigint NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (scope_kind, scope_id, key_id),
    CHECK (state IN (0, 1, 2)),
    CHECK (version > 0),
    CHECK (codec_version > 0)
);

CREATE TABLE IF NOT EXISTS settings_history (
    id bigserial PRIMARY KEY,
    scope_kind text NOT NULL,
    scope_id text NOT NULL,
    key_id text NOT NULL,
    action smallint NOT NULL,
    version bigint NOT NULL,
    codec_id text NOT NULL,
    codec_version integer NOT NULL,
    before_state smallint NOT NULL,
    before_value bytea,
    before_redacted boolean NOT NULL,
    after_state smallint NOT NULL,
    after_value bytea,
    after_redacted boolean NOT NULL,
    actor text NOT NULL,
    reason text NOT NULL,
    changed_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS settings_history_owner_key_idx
    ON settings_history (scope_kind, scope_id, key_id, id DESC);

CREATE TABLE IF NOT EXISTS settings_migrations (
    plan_id text NOT NULL,
    step_id text NOT NULL,
    scope_kind text NOT NULL,
    scope_id text NOT NULL,
    completed_at timestamptz NOT NULL,
    PRIMARY KEY (plan_id, step_id, scope_kind, scope_id)
);

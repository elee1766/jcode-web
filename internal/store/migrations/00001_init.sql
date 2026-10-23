-- +goose Up
CREATE TABLE events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    upstream_id    INTEGER NOT NULL,
    session_id     TEXT    NOT NULL,
    session_name   TEXT    NOT NULL DEFAULT '',
    swarm_id       TEXT    NOT NULL DEFAULT '',
    timestamp_unix INTEGER NOT NULL,
    type           TEXT    NOT NULL,
    payload_json   TEXT    NOT NULL,
    summary        TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX events_session_id ON events (session_id, id);
CREATE INDEX events_timestamp ON events (timestamp_unix);

CREATE TABLE push_subscriptions (
    endpoint   TEXT PRIMARY KEY,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE kv;
DROP TABLE push_subscriptions;
DROP TABLE events;

-- +goose Up
CREATE TABLE summaries (
    session_id TEXT PRIMARY KEY,
    text       TEXT    NOT NULL,
    model      TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE summaries;

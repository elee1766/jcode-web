-- +goose Up
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    metadata TEXT NOT NULL DEFAULT '{}',
    generation INTEGER NOT NULL DEFAULT 0,
    snapshot_key TEXT NOT NULL DEFAULT '',
    journal_key TEXT NOT NULL DEFAULT '',
    journal_inode INTEGER NOT NULL DEFAULT 0,
    journal_offset INTEGER NOT NULL DEFAULT 0,
    message_count INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE messages (
    session_id TEXT NOT NULL,
    generation INTEGER NOT NULL,
    message_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    PRIMARY KEY(session_id, generation, message_id)
) WITHOUT ROWID;
CREATE TABLE chunks (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    generation INTEGER NOT NULL,
    ordinal INTEGER NOT NULL,
    role TEXT NOT NULL,
    body TEXT NOT NULL,
    folded TEXT NOT NULL
);
CREATE INDEX chunks_session ON chunks(session_id, generation);
CREATE VIRTUAL TABLE chunks_fts USING fts5(folded, content='chunks', content_rowid='id', tokenize='trigram case_sensitive 1', detail=none);
-- +goose StatementBegin
CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, folded) VALUES (new.id, new.folded);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, folded) VALUES ('delete', old.id, old.folded);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER chunks_ad;
DROP TRIGGER chunks_ai;
DROP TABLE chunks_fts;
DROP TABLE chunks;
DROP TABLE messages;
DROP TABLE sessions;

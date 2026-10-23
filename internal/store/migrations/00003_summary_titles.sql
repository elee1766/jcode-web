-- +goose Up
ALTER TABLE summaries ADD COLUMN title TEXT NOT NULL DEFAULT '';
CREATE INDEX events_type_id ON events(type, id);

-- +goose Down
DROP INDEX events_type_id;
ALTER TABLE summaries DROP COLUMN title;

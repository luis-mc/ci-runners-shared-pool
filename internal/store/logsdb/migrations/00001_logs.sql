-- +goose Up
-- One row per log line. `day` is the UTC day number, so retention deletes by day (observability R6.8).
CREATE TABLE logs (
  id        INTEGER PRIMARY KEY,
  at_ms     INTEGER NOT NULL,
  day       INTEGER NOT NULL,
  level     INTEGER NOT NULL,
  machine   TEXT NOT NULL DEFAULT '',
  runner    TEXT NOT NULL DEFAULT '',
  job       TEXT NOT NULL DEFAULT '',
  component TEXT NOT NULL DEFAULT '',
  message   TEXT NOT NULL,
  attrs     TEXT NOT NULL DEFAULT '',
  bytes     INTEGER NOT NULL
);
CREATE INDEX logs_day ON logs (day);
CREATE INDEX logs_machine_at ON logs (machine, at_ms);
CREATE INDEX logs_at ON logs (at_ms);

-- Full text over the message (R5.21).
CREATE VIRTUAL TABLE logs_fts USING fts5(message, content='logs', content_rowid='id');
-- +goose StatementBegin
CREATE TRIGGER logs_ai AFTER INSERT ON logs BEGIN
  INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER logs_ad AFTER DELETE ON logs BEGIN
  INSERT INTO logs_fts(logs_fts, rowid, message) VALUES ('delete', old.id, old.message);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER logs_ad;
DROP TRIGGER logs_ai;
DROP TABLE logs_fts;
DROP TABLE logs;

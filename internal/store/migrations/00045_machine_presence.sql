-- +goose Up
-- CRSP263: uptime per machine (machines R5.83 to R5.86). One row each time the state of a machine changes:
-- taking (online and takes jobs), not_taking (online, takes none, with the reason) or offline (with the reason).
-- A row starts a period that lasts until the next row; the uptime and the fallback reasons are read from them.
CREATE TABLE machine_presence (
    machine_id TEXT NOT NULL,
    at         TEXT NOT NULL,
    state      TEXT NOT NULL CHECK (state IN ('taking', 'not_taking', 'offline')),
    reason     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (machine_id, at)
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE machine_presence;

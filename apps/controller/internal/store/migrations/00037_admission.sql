-- +goose Up
-- OPS74: admission by pool room and the reserved job of a machine
-- (scheduling R5.90 to R5.94). One reserved job per machine; passes are the
-- count of later jobs that started while the ticket waited.

CREATE TABLE sched_reservations (
    machine_id TEXT PRIMARY KEY,
    ticket_id  TEXT NOT NULL,
    cpus       INTEGER NOT NULL CHECK (cpus >= 1),
    mem_gib    INTEGER NOT NULL CHECK (mem_gib >= 1),
    class      TEXT NOT NULL DEFAULT '',
    since      TEXT NOT NULL,
    expected   TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE sched_passes (
    ticket_id  TEXT PRIMARY KEY,
    machine_id TEXT NOT NULL,
    passes     INTEGER NOT NULL DEFAULT 0 CHECK (passes >= 0)
) STRICT;

-- +goose Down
DROP TABLE sched_passes;
DROP TABLE sched_reservations;

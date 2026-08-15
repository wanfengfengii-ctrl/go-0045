package storage

// schemaSQL defines the full database schema. It is idempotent: every statement
// uses IF NOT EXISTS, so running it on an already-initialized database is a
// no-op. The partial unique indexes are the linchpin of the concurrency
// guarantees:
//
//   - idx_leases_slot_active: at most one non-terminal (active|releasing) lease
//     per slot. This is the database-level enforcement of "one slot, one lease".
//   - idx_credential_order_issued: at most one outstanding (issued) credential
//     per order.
//   - idx_commands_sequence: every device command sequence is globally unique,
//     which makes sends and receipt processing idempotent.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS slots (
    id               TEXT    PRIMARY KEY,
    version          INTEGER NOT NULL,
    status           TEXT    NOT NULL,
    current_lease_id TEXT    NOT NULL DEFAULT '',
    location         TEXT    NOT NULL DEFAULT '',
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
    id             TEXT    PRIMARY KEY,
    type           TEXT    NOT NULL,
    status         TEXT    NOT NULL,
    slot_id        TEXT    NOT NULL DEFAULT '',
    reader_id      TEXT    NOT NULL,
    book_id        TEXT    NOT NULL,
    lease_id       TEXT    NOT NULL DEFAULT '',
    credential_id  TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_reader ON orders(reader_id);

CREATE TABLE IF NOT EXISTS leases (
    id         TEXT    PRIMARY KEY,
    slot_id    TEXT    NOT NULL,
    order_id   TEXT    NOT NULL,
    epoch      INTEGER NOT NULL,
    status     TEXT    NOT NULL,
    version    INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_leases_slot ON leases(slot_id);
CREATE INDEX IF NOT EXISTS idx_leases_status ON leases(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_leases_slot_active
    ON leases(slot_id) WHERE status IN ('active','releasing');

CREATE TABLE IF NOT EXISTS credentials (
    id          TEXT    PRIMARY KEY,
    order_id    TEXT    NOT NULL,
    slot_id     TEXT    NOT NULL,
    hash        TEXT    NOT NULL UNIQUE,
    status      TEXT    NOT NULL,
    issued_at   INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    consumed_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_credentials_order ON credentials(order_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_credential_order_issued
    ON credentials(order_id) WHERE status = 'issued';

CREATE TABLE IF NOT EXISTS device_commands (
    id             TEXT    PRIMARY KEY,
    slot_id        TEXT    NOT NULL,
    lease_id       TEXT    NOT NULL,
    epoch          INTEGER NOT NULL,
    sequence       INTEGER NOT NULL,
    code           INTEGER NOT NULL,
    status         TEXT    NOT NULL,
    retries        INTEGER NOT NULL DEFAULT 0,
    version        INTEGER NOT NULL DEFAULT 0,
    deadline       INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    sent_at        INTEGER NOT NULL DEFAULT 0,
    acknowledged_at INTEGER NOT NULL DEFAULT 0,
    result         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_commands_status ON device_commands(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_commands_sequence ON device_commands(sequence);

CREATE TABLE IF NOT EXISTS seq (
    name TEXT PRIMARY KEY,
    val  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS audit (
    id         TEXT    PRIMARY KEY,
    event_type TEXT    NOT NULL,
    order_id   TEXT    NOT NULL DEFAULT '',
    lease_id   TEXT    NOT NULL DEFAULT '',
    slot_id    TEXT    NOT NULL DEFAULT '',
    sequence   INTEGER NOT NULL DEFAULT 0,
    result     TEXT    NOT NULL DEFAULT '',
    detail     TEXT    NOT NULL DEFAULT '',
    at         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_at ON audit(at);
CREATE INDEX IF NOT EXISTS idx_audit_order ON audit(order_id) WHERE order_id != '';
CREATE INDEX IF NOT EXISTS idx_audit_slot ON audit(slot_id) WHERE slot_id != '';
CREATE INDEX IF NOT EXISTS idx_audit_event ON audit(event_type);
`

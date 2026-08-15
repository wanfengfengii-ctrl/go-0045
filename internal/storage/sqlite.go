package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"modernc.org/sqlite"

	"library-locker-lease-controller/internal/domain"
)

// SQLite result-code masks (primary codes). Extended codes share the low byte.
const (
	codeConstraint = 19
	codeBusy       = 5
	codeLocked     = 6
	codeReadOnly   = 8
	codeIOErr      = 10
)

// Open returns a *sql.DB backed by the pure-Go modernc.org/sqlite driver for
// the file at path. The DSN configures immediate transactions, a generous busy
// timeout and WAL journaling so that concurrent writers serialize cleanly
// rather than failing with "database is locked".
func Open(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("storage: resolve path: %w", err)
	}
	q := url.Values{}
	q.Set("_txlock", "immediate")
	q.Set("_busy_timeout", "30000")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + abs + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, mapErr(err)
	}
	// A small connection pool lets independent reads overlap while writers
	// serialize on the IMMEDIATE lock.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(time.Minute)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, mapErr(err)
	}
	return db, nil
}

// Migrate applies the schema to db. It is idempotent.
func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, schemaSQL)
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// execer is the common surface implemented by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SQLiteStore is a Store backed by a *sql.DB. It embeds dbOps (which implements
// DB against the pool's auto-commit connection) and adds InTx/Close.
type SQLiteStore struct {
	db *sql.DB
	dbOps
}

// NewSQLiteStore wraps an already-open *sql.DB.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db, dbOps: dbOps{e: db}}
}

// OpenStore opens the database at path, migrates it and returns a ready Store.
func OpenStore(ctx context.Context, path string) (*SQLiteStore, error) {
	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return NewSQLiteStore(db), nil
}

// InTx runs fn inside a single transaction. The DSN's _txlock=immediate makes
// database/sql issue BEGIN IMMEDIATE, acquiring the write lock up front so the
// read-modify-write inside fn is atomic with respect to other writers. On any
// error from fn or the commit the transaction is rolled back.
func (s *SQLiteStore) InTx(ctx context.Context, fn func(DB) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrContextCanceled, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mapErr(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(dbOps{e: tx}); err != nil {
		return mapErr(err)
	}
	if err := tx.Commit(); err != nil {
		return mapErr(err)
	}
	committed = true
	return nil
}

// Close releases the underlying connection pool.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB (mainly for maintenance tooling).
func (s *SQLiteStore) DB() *sql.DB { return s.db }

// dbOps implements DB against either the connection pool or a transaction.
type dbOps struct {
	e execer
}

// mapErr translates driver and context errors into domain sentinels. It
// preserves the original error via %w so adapters can still inspect it.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", domain.ErrContextCanceled, err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %v", domain.ErrNotFound, err)
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		code := se.Code() & 0xFF
		switch code {
		case codeConstraint:
			return fmt.Errorf("%w: %v", domain.ErrLeaseConflict, err)
		case codeBusy, codeLocked, codeIOErr, codeReadOnly:
			return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
		}
	}
	return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
}

package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// pragmaValue queries a single PRAGMA on conn and returns its string value.
func pragmaValue(ctx context.Context, conn *sql.Conn, name string) (string, error) {
	var v string
	err := conn.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// newTestStore opens a fresh database under t.TempDir(), migrates it and
// returns the store. Close is registered with t.Cleanup.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestOpenAppliesDeclaredPragmas is the regression test for the bug where
// repeated url.Values.Set("_pragma", ...) collapsed to a single value, so
// only synchronous(NORMAL) took effect while journal_mode(WAL) and
// foreign_keys(1) were silently dropped from the DSN. The observed symptom
// was DELETE journaling with foreign keys off on every pooled connection.
//
// foreign_keys is a per-connection pragma that defaults to off, so reading it
// on several simultaneously-held distinct connections proves the pragma is
// applied to every connection the pool opens, not just the first. A barrier
// guarantees all connections are acquired (and thus distinct) before any is
// inspected. journal_mode, though persistent at the DB level, still
// distinguishes bug from fix: with the pragma dropped the database never leaves
// the default "delete" mode.
func TestOpenAppliesDeclaredPragmas(t *testing.T) {
	store := newTestStore(t)
	db := store.DB()

	const conns = 4
	ctx := context.Background()

	// Barrier: every goroutine first acquires its own *sql.Conn, then blocks
	// on release until all of them hold a connection, then reads the pragmas.
	// This guarantees the pool opens `conns` distinct connections.
	acquired := make(chan struct{}, conns)
	release := make(chan struct{})

	type result struct {
		journal, fk, sync string
		err               error
	}
	results := make([]result, conns)
	var wg sync.WaitGroup
	wg.Add(conns)
	for i := 0; i < conns; i++ {
		i := i
		go func() {
			defer wg.Done()
			conn, err := db.Conn(ctx)
			if err != nil {
				results[i].err = err
				return
			}
			defer conn.Close()
			acquired <- struct{}{}
			<-release
			// PRAGMA ... reads are not writes, so they do not contend even
			// under the buggy DELETE journal mode.
			jm, err := pragmaValue(ctx, conn, "journal_mode")
			if err != nil {
				results[i].err = err
				return
			}
			fk, err := pragmaValue(ctx, conn, "foreign_keys")
			if err != nil {
				results[i].err = err
				return
			}
			sy, err := pragmaValue(ctx, conn, "synchronous")
			if err != nil {
				results[i].err = err
				return
			}
			results[i] = result{journal: jm, fk: fk, sync: sy}
		}()
	}
	for i := 0; i < conns; i++ {
		<-acquired
	}
	close(release)
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("conn %d: %v", i, r.err)
		}
		if got := strings.ToLower(r.journal); got != "wal" {
			t.Errorf("conn %d journal_mode = %q, want %q", i, r.journal, "wal")
		}
		if r.fk != "1" {
			t.Errorf("conn %d foreign_keys = %q, want %q", i, r.fk, "1")
		}
		// synchronous(NORMAL) maps to the integer 1.
		if r.sync != "1" {
			t.Errorf("conn %d synchronous = %q, want %q", i, r.sync, "1")
		}
	}
}

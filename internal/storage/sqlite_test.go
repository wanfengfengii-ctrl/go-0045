package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmasToEveryConnection(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "storage.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	const connectionCount = 4
	for i := 0; i < connectionCount; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("db.Conn() for connection %d error = %v", i, err)
		}
		t.Cleanup(func() {
			if err := conn.Close(); err != nil {
				t.Errorf("connection %d Close() error = %v", i, err)
			}
		})

		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatalf("query journal_mode on connection %d: %v", i, err)
		}
		if journalMode != "wal" {
			t.Errorf("connection %d journal_mode = %q, want %q", i, journalMode, "wal")
		}

		var foreignKeys int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("query foreign_keys on connection %d: %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", i, foreignKeys)
		}

		var synchronous int
		if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatalf("query synchronous on connection %d: %v", i, err)
		}
		if synchronous != 1 {
			t.Errorf("connection %d synchronous = %d, want 1", i, synchronous)
		}
	}
}

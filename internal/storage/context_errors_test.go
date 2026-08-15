package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"library-locker-lease-controller/internal/domain"
)

// newTestStore opens a migrated SQLite database in a per-test temp directory.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// wantCanceled asserts that err reaches both domain.ErrContextCanceled and the
// standard library context sentinel, so the upper layer can distinguish
// cancellation from timeout and apply its audit/retry policy.
func wantCanceled(t *testing.T, err error, stdSentinel error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected non-nil error, got nil")
	}
	if !errors.Is(err, domain.ErrContextCanceled) {
		t.Errorf("errors.Is(err, domain.ErrContextCanceled) = false; want true\nerr: %v", err)
	}
	if !errors.Is(err, stdSentinel) {
		t.Errorf("errors.Is(err, %v) = false; want true\nerr: %v", stdSentinel, err)
	}
}

// contextScenarios returns the cancellation scenarios exercised by every test
// below. Each produces an already-expired context whose ctx.Err() is the named
// standard library sentinel.
func contextScenarios() []struct {
	name     string
	ctx      context.Context
	sentinel error
} {
	canceledCtx, cancel1 := context.WithCancel(context.Background())
	cancel1()

	deadlineCtx, cancel2 := context.WithDeadline(context.Background(), time.Now().Add(-time.Hour))
	_ = cancel2 // deadline already elapsed; cancel only silences the vet lostcancel check

	return []struct {
		name     string
		ctx      context.Context
		sentinel error
	}{
		{"canceled", canceledCtx, context.Canceled},
		{"deadline", deadlineCtx, context.DeadlineExceeded},
	}
}

// TestMapErr_ContextChain covers the storage error mapping contract: a context
// cancellation or timeout returned by SQLite/database/sql is wrapped so that
// both the domain sentinel and the original standard library error remain
// reachable via errors.Is, including when the driver wraps the context error.
func TestMapErr_ContextChain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       error
		sentinel error
	}{
		{"canceled", context.Canceled, context.Canceled},
		{"deadline", context.DeadlineExceeded, context.DeadlineExceeded},
		{"wrapped-canceled", fmt.Errorf("driver: %w", context.Canceled), context.Canceled},
		{"wrapped-deadline", fmt.Errorf("driver: %w", context.DeadlineExceeded), context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantCanceled(t, mapErr(tc.in), tc.sentinel)
		})
	}
}

// TestMapErr_OtherClassificationsUnchanged guards against the fix spilling into
// the other error branches. sql.ErrNoRows keeps mapping to ErrNotFound and does
// not expose ErrNoRows in the chain; a generic error keeps mapping to
// ErrStorageUnavailable.
func TestMapErr_OtherClassificationsUnchanged(t *testing.T) {
	t.Run("no-rows", func(t *testing.T) {
		err := mapErr(sql.ErrNoRows)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("errors.Is(err, domain.ErrNotFound) = false; want true\nerr: %v", err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			t.Errorf("errors.Is(err, sql.ErrNoRows) = true; want false (behavior unchanged)\nerr: %v", err)
		}
	})
	t.Run("generic", func(t *testing.T) {
		err := mapErr(errors.New("boom"))
		if !errors.Is(err, domain.ErrStorageUnavailable) {
			t.Errorf("errors.Is(err, domain.ErrStorageUnavailable) = false; want true\nerr: %v", err)
		}
	})
}

// TestDirectCall_ContextChain verifies the contract when a DB operation is
// called directly (auto-commit) against an already-expired context: the
// SQLite driver returns the standard library context error and mapErr wraps it
// so both sentinels stay reachable.
func TestDirectCall_ContextChain(t *testing.T) {
	store := newTestStore(t)
	for _, tc := range contextScenarios() {
		t.Run(tc.name, func(t *testing.T) {
			err := store.CreateSlot(tc.ctx,
				domain.LockerSlot{ID: "s1", Status: domain.SlotActive, Location: "A1"},
				time.Now())
			wantCanceled(t, err, tc.sentinel)
		})
	}
}

// TestSQLiteStore_InTx_ContextChain verifies the transaction wrapping contract:
// an already-expired context is rejected at the InTx pre-check, returning an
// error that reaches both the domain sentinel and the standard library error.
func TestSQLiteStore_InTx_ContextChain(t *testing.T) {
	store := newTestStore(t)
	for _, tc := range contextScenarios() {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			err := store.InTx(tc.ctx, func(DB) error {
				ran = true
				return nil
			})
			if ran {
				t.Error("transaction body ran; expected abort before BeginTx")
			}
			wantCanceled(t, err, tc.sentinel)
		})
	}
}

// TestFaultStore_InTx_ContextChain verifies the fault-wrapper contract: a
// canceled/expired context is rejected at the FaultStore.InTx pre-check with
// the same dual-sentinel chain, without consulting the injected faults or the
// inner store.
func TestFaultStore_InTx_ContextChain(t *testing.T) {
	store := newTestStore(t)
	fs := NewFaultStore(store, &Faults{})
	for _, tc := range contextScenarios() {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			err := fs.InTx(tc.ctx, func(DB) error {
				ran = true
				return nil
			})
			if ran {
				t.Error("transaction body ran; expected abort before inner InTx")
			}
			wantCanceled(t, err, tc.sentinel)
		})
	}
}

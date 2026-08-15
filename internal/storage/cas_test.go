package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"library-locker-lease-controller/internal/domain"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "locker.db")
	store, err := OpenStore(ctx, path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustCreateSlot(t *testing.T, store *SQLiteStore, id string) domain.LockerSlot {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	slot := domain.LockerSlot{ID: id, Status: domain.SlotActive}
	if err := store.CreateSlot(ctx, slot, now); err != nil {
		t.Fatalf("CreateSlot(%s): %v", id, err)
	}
	return slot
}

func mustCreateLease(t *testing.T, store *SQLiteStore, id, slotID string) domain.Lease {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	lease := domain.Lease{
		ID:        id,
		SlotID:    slotID,
		OrderID:   "order_" + id,
		Epoch:     1,
		Status:    domain.LeaseActive,
		Version:   1,
		ExpiresAt: now.Add(time.Hour),
	}
	if err := store.CreateLease(ctx, lease, now); err != nil {
		t.Fatalf("CreateLease(%s): %v", id, err)
	}
	return lease
}

// TestSlotUpdate_RoundTrip exercises the normal single-threaded
// read-mutate-save flow for every public slot state transition. A successful
// transition must persist the new state and bump the version exactly once.
func TestSlotUpdate_RoundTrip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	mustCreateSlot(t, store, "slot-1")

	// Assign a lease: read (v1) -> AssignLease (bump) -> save.
	slot, err := store.GetSlot(ctx, "slot-1")
	if err != nil {
		t.Fatalf("GetSlot: %v", err)
	}
	if slot.Version != 1 {
		t.Fatalf("initial version = %d, want 1", slot.Version)
	}
	if !slot.CanAcceptLease() {
		t.Fatal("fresh active slot must accept a lease")
	}
	slot.AssignLease("lease-1")
	updated, err := store.UpdateSlot(ctx, slot, now)
	if err != nil {
		t.Fatalf("UpdateSlot after AssignLease: %v", err)
	}
	if updated.Version != 2 {
		t.Errorf("returned version = %d, want 2", updated.Version)
	}
	reread, err := store.GetSlot(ctx, "slot-1")
	if err != nil {
		t.Fatalf("re-GetSlot: %v", err)
	}
	if reread.Version != 2 {
		t.Errorf("persisted version = %d, want 2", reread.Version)
	}
	if reread.CurrentLeaseID != "lease-1" {
		t.Errorf("persisted current_lease_id = %q, want lease-1", reread.CurrentLeaseID)
	}

	// Release: read (v2) -> Release (bump) -> save.
	slot, _ = store.GetSlot(ctx, "slot-1")
	slot.Release()
	if _, err := store.UpdateSlot(ctx, slot, now); err != nil {
		t.Fatalf("UpdateSlot after Release: %v", err)
	}
	reread, _ = store.GetSlot(ctx, "slot-1")
	if reread.Version != 3 || reread.CurrentLeaseID != "" {
		t.Errorf("after Release: version=%d current_lease_id=%q, want 3/empty", reread.Version, reread.CurrentLeaseID)
	}

	// Deactivate: read (v3) -> Deactivate (bump) -> save.
	slot, _ = store.GetSlot(ctx, "slot-1")
	slot.Deactivate()
	if _, err := store.UpdateSlot(ctx, slot, now); err != nil {
		t.Fatalf("UpdateSlot after Deactivate: %v", err)
	}
	reread, _ = store.GetSlot(ctx, "slot-1")
	if reread.Version != 4 || reread.Status != domain.SlotDeactivated {
		t.Errorf("after Deactivate: version=%d status=%q, want 4/deactivated", reread.Version, reread.Status)
	}

	// Reactivate: read (v4) -> Reactivate (bump) -> save.
	slot, _ = store.GetSlot(ctx, "slot-1")
	slot.Reactivate()
	if _, err := store.UpdateSlot(ctx, slot, now); err != nil {
		t.Fatalf("UpdateSlot after Reactivate: %v", err)
	}
	reread, _ = store.GetSlot(ctx, "slot-1")
	if reread.Version != 5 || reread.Status != domain.SlotActive {
		t.Errorf("after Reactivate: version=%d status=%q, want 5/active", reread.Version, reread.Status)
	}
}

// TestSlotUpdate_ConcurrentConflict simulates a real optimistic-lock
// collision: two readers read the same version, both mutate, the first save
// succeeds and the second (stale) save must be rejected with a recognizable
// conflict error while the database keeps the first writer's state.
func TestSlotUpdate_ConcurrentConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	mustCreateSlot(t, store, "slot-c")

	first, err := store.GetSlot(ctx, "slot-c")
	if err != nil {
		t.Fatalf("GetSlot first: %v", err)
	}
	second, err := store.GetSlot(ctx, "slot-c")
	if err != nil {
		t.Fatalf("GetSlot second: %v", err)
	}
	if first.Version != 1 || second.Version != 1 {
		t.Fatalf("read versions = %d/%d, want 1/1", first.Version, second.Version)
	}

	// Both mutate their in-memory copies independently.
	first.AssignLease("lease-wins")
	second.AssignLease("lease-stale")

	// First writer wins.
	if _, err := store.UpdateSlot(ctx, first, now); err != nil {
		t.Fatalf("first UpdateSlot: %v", err)
	}

	// Second writer's version is now stale; the CAS must reject it.
	_, err = store.UpdateSlot(ctx, second, now)
	if !errors.Is(err, domain.ErrLeaseConflict) {
		t.Fatalf("stale UpdateSlot error = %v, want a wrap of %v", err, domain.ErrLeaseConflict)
	}

	// The database must retain the first writer's state.
	got, err := store.GetSlot(ctx, "slot-c")
	if err != nil {
		t.Fatalf("final GetSlot: %v", err)
	}
	if got.Version != 2 || got.CurrentLeaseID != "lease-wins" {
		t.Errorf("after conflict: version=%d current_lease_id=%q, want 2/lease-wins", got.Version, got.CurrentLeaseID)
	}
}

// TestLeaseUpdate_RoundTrip exercises the normal single-threaded
// read-mutate-save flow for the public lease state transitions.
func TestLeaseUpdate_RoundTrip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	mustCreateSlot(t, store, "slot-l")
	mustCreateLease(t, store, "lease-1", "slot-l")

	// BeginRelease: active -> releasing.
	lease, err := store.GetLease(ctx, "lease-1")
	if err != nil {
		t.Fatalf("GetLease: %v", err)
	}
	if lease.Version != 1 {
		t.Fatalf("initial version = %d, want 1", lease.Version)
	}
	if err := lease.BeginRelease(); err != nil {
		t.Fatalf("BeginRelease: %v", err)
	}
	if _, err := store.UpdateLease(ctx, lease, now); err != nil {
		t.Fatalf("UpdateLease after BeginRelease: %v", err)
	}
	reread, _ := store.GetLease(ctx, "lease-1")
	if reread.Version != 2 || reread.Status != domain.LeaseReleasing {
		t.Errorf("after BeginRelease: version=%d status=%q, want 2/releasing", reread.Version, reread.Status)
	}

	// Release: releasing -> released (terminal).
	lease, _ = store.GetLease(ctx, "lease-1")
	if err := lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := store.UpdateLease(ctx, lease, now); err != nil {
		t.Fatalf("UpdateLease after Release: %v", err)
	}
	reread, _ = store.GetLease(ctx, "lease-1")
	if reread.Version != 3 || reread.Status != domain.LeaseReleased {
		t.Errorf("after Release: version=%d status=%q, want 3/released", reread.Version, reread.Status)
	}

	// A fresh active lease exercising Expire and Revoke on separate rows.
	mustCreateLease(t, store, "lease-exp", "slot-l2")
	mustCreateSlot(t, store, "slot-l2")
	lease, _ = store.GetLease(ctx, "lease-exp")
	if err := lease.Expire(); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if _, err := store.UpdateLease(ctx, lease, now); err != nil {
		t.Fatalf("UpdateLease after Expire: %v", err)
	}
	reread, _ = store.GetLease(ctx, "lease-exp")
	if reread.Version != 2 || reread.Status != domain.LeaseExpired {
		t.Errorf("after Expire: version=%d status=%q, want 2/expired", reread.Version, reread.Status)
	}

	mustCreateLease(t, store, "lease-rev", "slot-l3")
	mustCreateSlot(t, store, "slot-l3")
	lease, _ = store.GetLease(ctx, "lease-rev")
	if err := lease.Revoke(); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := store.UpdateLease(ctx, lease, now); err != nil {
		t.Fatalf("UpdateLease after Revoke: %v", err)
	}
	reread, _ = store.GetLease(ctx, "lease-rev")
	if reread.Version != 2 || reread.Status != domain.LeaseRevoked {
		t.Errorf("after Revoke: version=%d status=%q, want 2/revoked", reread.Version, reread.Status)
	}
}

// TestLeaseUpdate_ConcurrentConflict simulates a real optimistic-lock
// collision on a single lease row.
func TestLeaseUpdate_ConcurrentConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	mustCreateSlot(t, store, "slot-lc")
	mustCreateLease(t, store, "lease-lc", "slot-lc")

	first, err := store.GetLease(ctx, "lease-lc")
	if err != nil {
		t.Fatalf("GetLease first: %v", err)
	}
	second, err := store.GetLease(ctx, "lease-lc")
	if err != nil {
		t.Fatalf("GetLease second: %v", err)
	}
	if first.Version != 1 || second.Version != 1 {
		t.Fatalf("read versions = %d/%d, want 1/1", first.Version, second.Version)
	}

	// Both attempt the same transition on their stale copies.
	if err := first.BeginRelease(); err != nil {
		t.Fatalf("first BeginRelease: %v", err)
	}
	if err := second.BeginRelease(); err != nil {
		t.Fatalf("second BeginRelease: %v", err)
	}

	if _, err := store.UpdateLease(ctx, first, now); err != nil {
		t.Fatalf("first UpdateLease: %v", err)
	}

	_, err = store.UpdateLease(ctx, second, now)
	if !errors.Is(err, domain.ErrLeaseConflict) {
		t.Fatalf("stale UpdateLease error = %v, want a wrap of %v", err, domain.ErrLeaseConflict)
	}

	got, err := store.GetLease(ctx, "lease-lc")
	if err != nil {
		t.Fatalf("final GetLease: %v", err)
	}
	if got.Version != 2 || got.Status != domain.LeaseReleasing {
		t.Errorf("after conflict: version=%d status=%q, want 2/releasing", got.Version, got.Status)
	}
}

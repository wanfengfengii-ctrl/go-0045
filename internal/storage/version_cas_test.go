package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"library-locker-lease-controller/internal/domain"
)

func TestDomainMutationsPersistWithVersionCAS(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	now := time.Date(2026, time.August, 16, 12, 0, 0, 0, time.UTC)
	advance := func() time.Time {
		now = now.Add(time.Second)
		return now
	}

	if err := store.CreateSlot(ctx, domain.LockerSlot{
		ID: "slot-lifecycle", Status: domain.SlotActive, Location: "A-01",
	}, advance()); err != nil {
		t.Fatalf("create lifecycle slot: %v", err)
	}
	checkSlotMutation := func(name string, mutate func(*domain.LockerSlot), wantStatus domain.SlotStatus, wantLeaseID string) {
		t.Helper()
		slot, err := store.GetSlot(ctx, "slot-lifecycle")
		if err != nil {
			t.Fatalf("%s: get slot: %v", name, err)
		}
		oldVersion := slot.Version
		mutate(&slot)
		if slot.Version != oldVersion+1 {
			t.Fatalf("%s: domain version = %d, want %d", name, slot.Version, oldVersion+1)
		}
		updated, err := store.UpdateSlot(ctx, slot, advance())
		if err != nil {
			t.Fatalf("%s: update slot: %v", name, err)
		}
		if updated.Version != slot.Version {
			t.Fatalf("%s: returned version = %d, want %d", name, updated.Version, slot.Version)
		}
		persisted, err := store.GetSlot(ctx, slot.ID)
		if err != nil {
			t.Fatalf("%s: reload slot: %v", name, err)
		}
		if persisted.Status != wantStatus || persisted.CurrentLeaseID != wantLeaseID || persisted.Version != slot.Version {
			t.Fatalf("%s: persisted slot = {status:%s lease:%q version:%d}, want {status:%s lease:%q version:%d}",
				name, persisted.Status, persisted.CurrentLeaseID, persisted.Version,
				wantStatus, wantLeaseID, slot.Version)
		}
	}
	checkSlotMutation("assign", func(slot *domain.LockerSlot) { slot.AssignLease("lease-lifecycle") }, domain.SlotActive, "lease-lifecycle")
	checkSlotMutation("release", func(slot *domain.LockerSlot) { slot.Release() }, domain.SlotActive, "")
	checkSlotMutation("deactivate", func(slot *domain.LockerSlot) { slot.Deactivate() }, domain.SlotDeactivated, "")
	checkSlotMutation("reactivate", func(slot *domain.LockerSlot) { slot.Reactivate() }, domain.SlotActive, "")

	createLease := func(id, slotID string) {
		t.Helper()
		at := advance()
		if err := store.CreateLease(ctx, domain.Lease{
			ID: id, SlotID: slotID, OrderID: "order-" + id, Epoch: 1,
			Status: domain.LeaseActive, Version: 1, CreatedAt: at,
			ExpiresAt: at.Add(time.Hour), UpdatedAt: at,
		}, at); err != nil {
			t.Fatalf("create lease %s: %v", id, err)
		}
	}
	checkLeaseMutation := func(name, id string, mutate func(*domain.Lease) error, wantStatus domain.LeaseStatus) {
		t.Helper()
		lease, err := store.GetLease(ctx, id)
		if err != nil {
			t.Fatalf("%s: get lease: %v", name, err)
		}
		oldVersion := lease.Version
		if err := mutate(&lease); err != nil {
			t.Fatalf("%s: domain mutation: %v", name, err)
		}
		if lease.Version != oldVersion+1 {
			t.Fatalf("%s: domain version = %d, want %d", name, lease.Version, oldVersion+1)
		}
		updated, err := store.UpdateLease(ctx, lease, advance())
		if err != nil {
			t.Fatalf("%s: update lease: %v", name, err)
		}
		if updated.Version != lease.Version || updated.Status != wantStatus {
			t.Fatalf("%s: updated lease = {status:%s version:%d}, want {status:%s version:%d}",
				name, updated.Status, updated.Version, wantStatus, lease.Version)
		}
		persisted, err := store.GetLease(ctx, id)
		if err != nil {
			t.Fatalf("%s: reload lease: %v", name, err)
		}
		if persisted.Status != wantStatus || persisted.Version != lease.Version {
			t.Fatalf("%s: persisted lease = {status:%s version:%d}, want {status:%s version:%d}",
				name, persisted.Status, persisted.Version, wantStatus, lease.Version)
		}
	}

	createLease("lease-release", "slot-release")
	checkLeaseMutation("begin release", "lease-release", (*domain.Lease).BeginRelease, domain.LeaseReleasing)
	checkLeaseMutation("complete release", "lease-release", (*domain.Lease).Release, domain.LeaseReleased)
	createLease("lease-expire", "slot-expire")
	checkLeaseMutation("expire", "lease-expire", (*domain.Lease).Expire, domain.LeaseExpired)
	createLease("lease-revoke", "slot-revoke")
	checkLeaseMutation("revoke", "lease-revoke", (*domain.Lease).Revoke, domain.LeaseRevoked)

	if err := store.CreateSlot(ctx, domain.LockerSlot{ID: "slot-conflict", Status: domain.SlotActive}, advance()); err != nil {
		t.Fatalf("create conflict slot: %v", err)
	}
	slotWinner, err := store.GetSlot(ctx, "slot-conflict")
	if err != nil {
		t.Fatalf("get winning slot copy: %v", err)
	}
	slotStale, err := store.GetSlot(ctx, "slot-conflict")
	if err != nil {
		t.Fatalf("get stale slot copy: %v", err)
	}
	slotWinner.AssignLease("lease-winner")
	slotStale.Deactivate()
	if _, err := store.UpdateSlot(ctx, slotWinner, advance()); err != nil {
		t.Fatalf("update winning slot copy: %v", err)
	}
	if _, err := store.UpdateSlot(ctx, slotStale, advance()); !errors.Is(err, domain.ErrLeaseConflict) {
		t.Fatalf("stale slot update error = %v, want ErrLeaseConflict", err)
	}
	persistedSlot, err := store.GetSlot(ctx, "slot-conflict")
	if err != nil {
		t.Fatalf("reload conflict slot: %v", err)
	}
	if persistedSlot.Status != domain.SlotActive || persistedSlot.CurrentLeaseID != "lease-winner" || persistedSlot.Version != slotWinner.Version {
		t.Fatalf("stale update changed slot: %+v", persistedSlot)
	}

	createLease("lease-conflict", "slot-conflict-lease")
	leaseWinner, err := store.GetLease(ctx, "lease-conflict")
	if err != nil {
		t.Fatalf("get winning lease copy: %v", err)
	}
	leaseStale, err := store.GetLease(ctx, "lease-conflict")
	if err != nil {
		t.Fatalf("get stale lease copy: %v", err)
	}
	if err := leaseWinner.BeginRelease(); err != nil {
		t.Fatalf("mutate winning lease copy: %v", err)
	}
	if err := leaseStale.Revoke(); err != nil {
		t.Fatalf("mutate stale lease copy: %v", err)
	}
	if _, err := store.UpdateLease(ctx, leaseWinner, advance()); err != nil {
		t.Fatalf("update winning lease copy: %v", err)
	}
	if _, err := store.UpdateLease(ctx, leaseStale, advance()); !errors.Is(err, domain.ErrLeaseConflict) {
		t.Fatalf("stale lease update error = %v, want ErrLeaseConflict", err)
	}
	persistedLease, err := store.GetLease(ctx, "lease-conflict")
	if err != nil {
		t.Fatalf("reload conflict lease: %v", err)
	}
	if persistedLease.Status != domain.LeaseReleasing || persistedLease.Version != leaseWinner.Version {
		t.Fatalf("stale update changed lease: %+v", persistedLease)
	}
}

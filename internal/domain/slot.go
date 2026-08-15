package domain

import "time"

// SlotStatus is the lifecycle state of a physical locker slot. Deactivation
// only prevents new leases; it never force-revokes an in-flight lease.
type SlotStatus string

const (
	SlotActive      SlotStatus = "active"
	SlotDeactivated SlotStatus = "deactivated"
)

// LockerSlot is a physical compartment in the book locker. The Version field is
// a monotonic optimistic-concurrency token: every mutation bumps it, and the
// storage layer applies updates with a WHERE version = ? guard so that
// concurrent allocations racing on the same slot are rejected.
type LockerSlot struct {
	ID             string     `json:"id"`
	Version        int64      `json:"version"`
	Status         SlotStatus `json:"status"`
	CurrentLeaseID string     `json:"current_lease_id,omitempty"`
	Location       string     `json:"location,omitempty"`
}

// CanAcceptLease reports whether the slot may be assigned a new lease. A slot
// can only accept a lease when it is active and has no current lease.
func (s LockerSlot) CanAcceptLease() bool {
	return s.Status == SlotActive && s.CurrentLeaseID == ""
}

// AssignLease binds a lease to the slot and bumps the version. The caller must
// have already verified CanAcceptLease.
func (s *LockerSlot) AssignLease(leaseID string) {
	s.CurrentLeaseID = leaseID
	s.Version++
}

// Release clears the current lease and bumps the version, making the slot
// available again (unless it has been deactivated in the meantime).
func (s *LockerSlot) Release() {
	s.CurrentLeaseID = ""
	s.Version++
}

// Deactivate marks the slot as unavailable for new leases and bumps the
// version. It does NOT touch an in-flight lease; the lease continues until it
// terminates naturally.
func (s *LockerSlot) Deactivate() {
	s.Status = SlotDeactivated
	s.Version++
}

// Reactivate makes a deactivated slot available again.
func (s *LockerSlot) Reactivate() {
	s.Status = SlotActive
	s.Version++
}

// CreatedAt/UpdatedAt are not part of the core invariant but are useful for
// audits; they are recorded by the storage layer.
type SlotAuditMeta struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

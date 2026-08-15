package domain

import "time"

// LeaseStatus is the lifecycle state of a slot lease.
type LeaseStatus string

const (
	// LeaseActive: a credential has been redeemed, the lease holds the slot.
	LeaseActive LeaseStatus = "active"
	// LeaseReleasing: the door has opened and the reader is performing the
	// handover/return; the slot is still occupied.
	LeaseReleasing LeaseStatus = "releasing"
	// LeaseReleased: the reader confirmed the handover/return; the slot is free.
	LeaseReleased LeaseStatus = "released"
	// LeaseExpired: the lease timed out before the reader confirmed.
	LeaseExpired LeaseStatus = "expired"
	// LeaseRevoked: the order was cancelled (or the device rejected the open
	// command) and the lease was forcibly ended.
	LeaseRevoked LeaseStatus = "revoked"
)

// IsTerminalLease reports whether the status is an irreversible end state. The
// partial unique index on the leases table covers exactly the non-terminal
// states (active, releasing) so that at most one such lease exists per slot.
func IsTerminalLease(s LeaseStatus) bool {
	switch s {
	case LeaseReleased, LeaseExpired, LeaseRevoked:
		return true
	}
	return false
}

// Lease is the exclusive hold on a slot for a single order. Epoch is a per-slot
// monotonic counter: every new lease for a slot gets a higher epoch, which lets
// the coordinator reject stale device receipts that reference a superseded
// lease. Version is the row-level optimistic-concurrency token.
type Lease struct {
	ID        string      `json:"id"`
	SlotID    string      `json:"slot_id"`
	OrderID   string      `json:"order_id"`
	Epoch     int64       `json:"epoch"`
	Status    LeaseStatus `json:"status"`
	Version   int64       `json:"version"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// NonTerminal reports whether the lease is still occupying the slot.
func (l Lease) NonTerminal() bool {
	return !IsTerminalLease(l.Status)
}

// IsExpiredAt reports whether the lease has passed its expiry time. The caller
// is responsible for transitioning the lease to LeaseExpired when true.
func (l Lease) IsExpiredAt(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && now.After(l.ExpiresAt)
}

// BeginRelease transitions an active lease to the releasing state and bumps the
// version. It is called once the door has been opened.
func (l *Lease) BeginRelease() error {
	if l.Status != LeaseActive {
		return ErrLeaseConflict
	}
	l.Status = LeaseReleasing
	l.Version++
	return nil
}

// Release transitions a releasing lease to the released terminal state.
func (l *Lease) Release() error {
	if l.Status != LeaseActive && l.Status != LeaseReleasing {
		return ErrLeaseConflict
	}
	l.Status = LeaseReleased
	l.Version++
	return nil
}

// Expire transitions a non-terminal lease to the expired terminal state.
func (l *Lease) Expire() error {
	if IsTerminalLease(l.Status) {
		return ErrLeaseConflict
	}
	l.Status = LeaseExpired
	l.Version++
	return nil
}

// Revoke transitions a non-terminal lease to the revoked terminal state.
func (l *Lease) Revoke() error {
	if IsTerminalLease(l.Status) {
		return ErrLeaseConflict
	}
	l.Status = LeaseRevoked
	l.Version++
	return nil
}

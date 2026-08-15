// Package domain defines the core entities, state machine rules and
// distinguishable error semantics for the library locker lease controller.
//
// The domain layer is free of I/O and persistence concerns. It describes the
// invariants that the coordinator enforces: at most one non-terminal lease per
// slot, one-time credential consumption, idempotent device commands and the
// terminal/irreversible nature of completed operations.
package domain

import "errors"

// Sentinel domain errors. Callers MUST distinguish between them with errors.Is
// rather than string matching. The HTTP/CLI adapters map each to a stable
// error code. Error context is never swallowed: wrappers use fmt.Errorf with
// %w so that errors.Is keeps resolving to the sentinels below.
var (
	// ErrCredentialExpired is returned when a credential is presented after its
	// expiry time has passed.
	ErrCredentialExpired = errors.New("credential expired")

	// ErrCredentialUsed is returned when a credential has already been consumed.
	ErrCredentialUsed = errors.New("credential already used")

	// ErrCredentialMismatch is returned when a credential does not bind to the
	// expected order or slot.
	ErrCredentialMismatch = errors.New("credential does not match order or slot")

	// ErrDeviceRejected is returned when the locker hardware rejected an
	// open-door command.
	ErrDeviceRejected = errors.New("device rejected")

	// ErrDeviceTimeout is returned when an open-door command did not receive a
	// receipt before its retry budget was exhausted.
	ErrDeviceTimeout = errors.New("device timeout")

	// ErrFrameCorrupt is returned by the frame codec when a frame cannot be
	// decoded or fails its CRC check.
	ErrFrameCorrupt = errors.New("frame corrupt")

	// ErrFrameTooLarge is returned when an encoded or decoded frame exceeds the
	// configured maximum payload length.
	ErrFrameTooLarge = errors.New("frame too large")

	// ErrStorageUnavailable is returned when the persistence layer is
	// unavailable or a fault was injected.
	ErrStorageUnavailable = errors.New("storage unavailable")

	// ErrContextCanceled is returned when an operation was aborted because its
	// context was canceled. It wraps context.Canceled so errors.Is keeps
	// working for both.
	ErrContextCanceled = errors.New("context canceled")

	// ErrUnknownSequence is returned when a device receipt references a
	// sequence number with no persisted command.
	ErrUnknownSequence = errors.New("unknown device sequence")

	// ErrDuplicateReceipt is returned when a device receipt is received for a
	// command that is already in a terminal state.
	ErrDuplicateReceipt = errors.New("duplicate device receipt")

	// ErrSlotDeactivated is returned when an operation requires an active slot
	// but the slot has been deactivated.
	ErrSlotDeactivated = errors.New("slot deactivated")

	// ErrSlotOccupied is returned when a slot already holds a non-terminal
	// lease and cannot accept a new one.
	ErrSlotOccupied = errors.New("slot occupied")

	// ErrNoFreeSlot is returned when no active slot is available to satisfy a
	// new order.
	ErrNoFreeSlot = errors.New("no free slot")

	// ErrLeaseConflict is returned when a CAS guard rejects a transition
	// because the lease version changed underneath the caller.
	ErrLeaseConflict = errors.New("lease conflict")

	// ErrLeaseExpired is returned when a lease is already in the expired
	// terminal state.
	ErrLeaseExpired = errors.New("lease expired")

	// ErrLeaseRevoked is returned when a lease is already revoked.
	ErrLeaseRevoked = errors.New("lease revoked")

	// ErrOrderInvalidState is returned when an operation is requested against
	// an order whose current state does not permit it.
	ErrOrderInvalidState = errors.New("order in invalid state for operation")

	// ErrNotFound is returned when a referenced entity does not exist.
	ErrNotFound = errors.New("not found")
)

// IsTerminal reports whether err is one of the terminal/irrecoverable domain
// errors that adapters surface without retrying.
func IsTerminal(err error) bool {
	switch {
	case errors.Is(err, ErrLeaseExpired),
		errors.Is(err, ErrLeaseRevoked),
		errors.Is(err, ErrDeviceRejected),
		errors.Is(err, ErrDeviceTimeout):
		return true
	}
	return false
}

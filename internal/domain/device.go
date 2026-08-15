package domain

import "time"

// CommandCode is the operation requested of the locker hardware.
type CommandCode byte

const (
	CmdOpenDoor  CommandCode = 0x01
	CmdCloseDoor CommandCode = 0x02
	CmdQueryDoor CommandCode = 0x03
)

// String returns a human-readable name for the command code.
func (c CommandCode) String() string {
	switch c {
	case CmdOpenDoor:
		return "open_door"
	case CmdCloseDoor:
		return "close_door"
	case CmdQueryDoor:
		return "query_door"
	default:
		return "unknown"
	}
}

// DeviceCommandStatus is the lifecycle state of an outbound device command.
type DeviceCommandStatus string

const (
	CmdQueued           DeviceCommandStatus = "queued"
	CmdSent             DeviceCommandStatus = "sent"
	CmdAcknowledged     DeviceCommandStatus = "acknowledged"
	CmdRejected         DeviceCommandStatus = "rejected"
	CmdTimeout          DeviceCommandStatus = "timeout"
	CmdUnknownSequence  DeviceCommandStatus = "unknown_sequence"
	CmdDuplicateReceipt DeviceCommandStatus = "duplicate_receipt"
)

// IsTerminalCommand reports whether the status is an irreversible end state.
// Terminal commands are never re-sent or re-processed by the scheduler.
func IsTerminalCommand(s DeviceCommandStatus) bool {
	switch s {
	case CmdAcknowledged, CmdRejected, CmdTimeout, CmdUnknownSequence, CmdDuplicateReceipt:
		return true
	}
	return false
}

// DeviceCommand is an outbound command to the locker hardware. The Sequence is
// a globally unique, persisted 4-byte counter echoed by the device in its
// receipt; it makes both re-sends and receipt processing idempotent. Version is
// the row-level optimistic-concurrency token used by the storage layer.
type DeviceCommand struct {
	ID             string              `json:"id"`
	SlotID         string              `json:"slot_id"`
	LeaseID        string              `json:"lease_id"`
	Epoch          int64               `json:"epoch"`
	Sequence       uint32              `json:"sequence"`
	Code           CommandCode         `json:"code"`
	Status         DeviceCommandStatus `json:"status"`
	Retries        int                 `json:"retries"`
	Version        int64               `json:"-"`
	Deadline       time.Time           `json:"deadline,omitempty"`
	CreatedAt      time.Time           `json:"created_at"`
	SentAt         time.Time           `json:"sent_at,omitempty"`
	AcknowledgedAt time.Time           `json:"acknowledged_at,omitempty"`
	Result         string              `json:"result,omitempty"`
}

// MarkSent records that the command was transmitted to the device and sets the
// receipt deadline.
func (d *DeviceCommand) MarkSent(now, deadline time.Time) {
	d.Status = CmdSent
	d.SentAt = now
	d.Deadline = deadline
}

// BumpRetry increments the retry counter and refreshes the send/deadline times
// for a retransmission. The sequence stays the same so the operation remains
// idempotent on the device side.
func (d *DeviceCommand) BumpRetry(now, deadline time.Time) {
	d.Retries++
	d.SentAt = now
	d.Deadline = deadline
}

// Acknowledge transitions a sent command to the acknowledged terminal state.
func (d *DeviceCommand) Acknowledge(now time.Time, result string) {
	d.Status = CmdAcknowledged
	d.AcknowledgedAt = now
	d.Result = result
}

// Reject transitions a sent command to the rejected terminal state.
func (d *DeviceCommand) Reject(now time.Time, result string) {
	d.Status = CmdRejected
	d.AcknowledgedAt = now
	d.Result = result
}

// TimeOut transitions a sent command to the timeout terminal state.
func (d *DeviceCommand) TimeOut(now time.Time) {
	d.Status = CmdTimeout
	d.AcknowledgedAt = now
	d.Result = "timeout"
}

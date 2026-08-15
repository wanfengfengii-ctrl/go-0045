package domain

import "time"

// AuditEventType categorizes an audit record.
type AuditEventType string

const (
	AuditOrderCreated       AuditEventType = "order_created"
	AuditSlotAssigned       AuditEventType = "slot_assigned"
	AuditCredentialIssued   AuditEventType = "credential_issued"
	AuditCredentialRedeemed AuditEventType = "credential_redeemed"
	AuditCredentialRejected AuditEventType = "credential_rejected"
	AuditCommandQueued      AuditEventType = "command_queued"
	AuditCommandSent        AuditEventType = "command_sent"
	AuditDoorOpened         AuditEventType = "door_opened"
	AuditDoorRejected       AuditEventType = "door_rejected"
	AuditCommandTimeout     AuditEventType = "command_timeout"
	AuditHandoverConfirmed  AuditEventType = "handover_confirmed"
	AuditReturnConfirmed    AuditEventType = "return_confirmed"
	AuditOrderCancelled     AuditEventType = "order_cancelled"
	AuditSlotDeactivated    AuditEventType = "slot_deactivated"
	AuditSlotReactivated    AuditEventType = "slot_reactivated"
	AuditLeaseExpired       AuditEventType = "lease_expired"
	AuditLeaseRevoked       AuditEventType = "lease_revoked"
	AuditLeaseReleased      AuditEventType = "lease_released"
	AuditUnknownSequence    AuditEventType = "unknown_sequence"
	AuditDuplicateReceipt   AuditEventType = "duplicate_receipt"
	AuditLateReceipt        AuditEventType = "late_receipt"
	AuditRecovery           AuditEventType = "recovery"
)

// AuditRecord is an append-only trace of every state transition and device
// interaction. Records are written inside the same transaction as the state
// change they describe, so the audit trail is consistent with the data.
type AuditRecord struct {
	ID        string          `json:"id"`
	EventType AuditEventType  `json:"event_type"`
	OrderID   string          `json:"order_id,omitempty"`
	LeaseID   string          `json:"lease_id,omitempty"`
	SlotID    string          `json:"slot_id,omitempty"`
	Sequence  uint32          `json:"sequence,omitempty"`
	Result    string          `json:"result,omitempty"`
	Detail    string          `json:"detail,omitempty"`
	At        time.Time       `json:"at"`
}

// AuditResult values describe the outcome of a device interaction.
const (
	ResultSuccess   = "success"
	ResultRejected  = "rejected"
	ResultTimeout   = "timeout"
	ResultUnknown   = "unknown"
	ResultDuplicate = "duplicate"
	ResultLate      = "late"
)

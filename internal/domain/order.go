package domain

import "time"

// OrderType distinguishes a pickup (reader collects a held book) from a return
// (reader drops a book off). The two flows share most of the state machine but
// terminate differently.
type OrderType string

const (
	OrderPickup OrderType = "pickup"
	OrderReturn OrderType = "return"
)

// OrderStatus is the lifecycle state of a loan order. States marked terminal
// are irreversible: once reached, the order never transitions again.
type OrderStatus string

const (
	OrderCreated         OrderStatus = "created"
	OrderSlotAssigned    OrderStatus = "slot_assigned"
	OrderCredentialIssued OrderStatus = "credential_issued"
	OrderRedeemed        OrderStatus = "redeemed"
	OrderDoorOpening     OrderStatus = "door_opening"
	OrderDoorOpened      OrderStatus = "door_opened"
	// OrderHandedOver is the success terminal for a pickup: the reader took the
	// book and confirmed the handover.
	OrderHandedOver OrderStatus = "handed_over"
	// OrderReturned is the success terminal for a return: the reader placed the
	// book and confirmed the return.
	OrderReturned OrderStatus = "returned"
	// Terminal failure states.
	OrderCancelled      OrderStatus = "cancelled"
	OrderExpired        OrderStatus = "expired"
	OrderDeviceRejected OrderStatus = "device_rejected"
	OrderDeviceTimeout  OrderStatus = "device_timeout"
)

// IsTerminalOrder reports whether the status is an irreversible end state.
func IsTerminalOrder(s OrderStatus) bool {
	switch s {
	case OrderHandedOver, OrderReturned, OrderCancelled, OrderExpired,
		OrderDeviceRejected, OrderDeviceTimeout:
		return true
	}
	return false
}

// LoanOrder ties a reader and a book to a slot assignment and the resulting
// lease/credential chain.
type LoanOrder struct {
	ID         string     `json:"id"`
	Type       OrderType  `json:"type"`
	Status     OrderStatus `json:"status"`
	SlotID     string     `json:"slot_id,omitempty"`
	ReaderID   string     `json:"reader_id"`
	BookID     string     `json:"book_id"`
	LeaseID    string     `json:"lease_id,omitempty"`
	CredentialID string   `json:"credential_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// CanCancel reports whether the order may still be cancelled. Terminal orders
// cannot be cancelled.
func (o LoanOrder) CanCancel() bool {
	return !IsTerminalOrder(o.Status)
}

// CanConfirm reports whether the reader may confirm handover/return, i.e. the
// door has been opened and the order awaits the reader's confirmation.
func (o LoanOrder) CanConfirm() bool {
	return o.Status == OrderDoorOpened
}

// Terminal reports whether the order has reached an irreversible state.
func (o LoanOrder) Terminal() bool {
	return IsTerminalOrder(o.Status)
}

package domain

import "fmt"

// Transition validates a requested order status change against the state
// machine. It returns ErrOrderInvalidState when the move is not allowed.
//
// Pickup flow:
//
//	created -> slot_assigned -> credential_issued -> redeemed ->
//	door_opening -> door_opened -> handed_over
//
// Return flow:
//
//	created -> slot_assigned -> credential_issued -> redeemed ->
//	door_opening -> door_opened -> returned
//
// Any non-terminal state may move to cancelled, expired, device_rejected or
// device_timeout. Terminal states accept no further transitions.
func Transition(from, to OrderStatus) error {
	if IsTerminalOrder(from) {
		return fmt.Errorf("%w: %s is terminal", ErrOrderInvalidState, from)
	}
	if from == to {
		return nil
	}
	// Terminal failure targets are valid from any non-terminal source.
	switch to {
	case OrderCancelled, OrderExpired, OrderDeviceRejected, OrderDeviceTimeout:
		return nil
	}
	allowed := map[OrderStatus]map[OrderStatus]struct{}{
		OrderCreated:          {OrderSlotAssigned: {}},
		OrderSlotAssigned:     {OrderCredentialIssued: {}},
		OrderCredentialIssued: {OrderRedeemed: {}},
		OrderRedeemed:         {OrderDoorOpening: {}},
		OrderDoorOpening:      {OrderDoorOpened: {}, OrderDeviceRejected: {}, OrderDeviceTimeout: {}},
		OrderDoorOpened:       {OrderHandedOver: {}, OrderReturned: {}},
	}
	next, ok := allowed[from]
	if !ok {
		return fmt.Errorf("%w: no transition from %s", ErrOrderInvalidState, from)
	}
	if _, ok := next[to]; !ok {
		return fmt.Errorf("%w: %s -> %s not allowed", ErrOrderInvalidState, from, to)
	}
	// The success terminal must match the order type; enforced by the caller
	// (coordinator), not here, to keep the table type-agnostic.
	return nil
}

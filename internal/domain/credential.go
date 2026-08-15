package domain

import "time"

// CredentialStatus is the lifecycle state of a one-time credential.
type CredentialStatus string

const (
	CredentialIssued  CredentialStatus = "issued"
	CredentialConsumed CredentialStatus = "consumed"
	CredentialExpired CredentialStatus = "expired"
)

// OneTimeCredential binds a single-use token (persisted only as an irreversible
// hash) to a specific order and slot with a hard expiry. The plaintext token is
// returned to the reader exactly once at issue time and is never stored.
type OneTimeCredential struct {
	ID         string           `json:"id"`
	OrderID    string           `json:"order_id"`
	SlotID     string           `json:"slot_id"`
	Hash       string           `json:"hash"`
	Status     CredentialStatus `json:"status"`
	IssuedAt   time.Time        `json:"issued_at"`
	ExpiresAt  time.Time        `json:"expires_at"`
	ConsumedAt time.Time        `json:"consumed_at,omitempty"`
}

// IsExpiredAt reports whether the credential has passed its expiry.
func (c OneTimeCredential) IsExpiredAt(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && now.After(c.ExpiresAt)
}

// Consume transitions an issued credential to the consumed state. It refuses if
// the credential is not in the issued state, which (combined with a CAS update
// in storage) guarantees single-use semantics even under concurrency.
func (c *OneTimeCredential) Consume(at time.Time) error {
	if c.Status == CredentialConsumed {
		return ErrCredentialUsed
	}
	if c.Status == CredentialExpired {
		return ErrCredentialExpired
	}
	if c.Status != CredentialIssued {
		return ErrCredentialMismatch
	}
	c.Status = CredentialConsumed
	c.ConsumedAt = at
	return nil
}

// MatchesOrder reports whether the credential binds to the given order and slot.
func (c OneTimeCredential) MatchesOrder(orderID, slotID string) bool {
	return c.OrderID == orderID && c.SlotID == slotID
}

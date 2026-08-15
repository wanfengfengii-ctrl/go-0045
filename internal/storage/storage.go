// Package storage provides the SQLite persistence layer for the locker lease
// controller.
//
// The package exposes a DB interface of fine-grained data operations and a
// Store that can run a set of operations atomically inside a transaction via
// InTx. Every mutation that participates in a state transition is written
// through the same transaction so that slots, orders, leases, credentials,
// device commands and audit records always move together. A slot version CAS
// and several partial unique indexes back the coordinator's per-slot
// serialization at the database level.
package storage

import (
	"context"
	"time"

	"library-locker-lease-controller/internal/domain"
)

// OrderFilter scopes a ListOrders query. A zero-valued field means "no filter".
type OrderFilter struct {
	Status   string
	ReaderID string
	SlotID   string
	Limit    int
}

// LeaseFilter scopes a ListLeases query.
type LeaseFilter struct {
	Status string
	SlotID string
	Limit  int
}

// AuditFilter scopes a ListAudit query.
type AuditFilter struct {
	OrderID   string
	SlotID    string
	EventType string
	Limit     int
}

// DB is the set of data operations available both as auto-commit calls on a
// Store and inside an InTx transaction. Every method accepts a context so that
// cancellation propagates; a canceled context yields an error wrapping
// domain.ErrContextCanceled.
type DB interface {
	// Slots
	CreateSlot(ctx context.Context, slot domain.LockerSlot, now time.Time) error
	GetSlot(ctx context.Context, id string) (domain.LockerSlot, error)
	ListSlots(ctx context.Context, status string) ([]domain.LockerSlot, error)
	FindFreeSlot(ctx context.Context) (domain.LockerSlot, error)
	UpdateSlot(ctx context.Context, slot domain.LockerSlot, now time.Time) (domain.LockerSlot, error)

	// Orders
	CreateOrder(ctx context.Context, order domain.LoanOrder) error
	GetOrder(ctx context.Context, id string) (domain.LoanOrder, error)
	UpdateOrderStatus(ctx context.Context, id string, to domain.OrderStatus, now time.Time) error
	ListOrders(ctx context.Context, f OrderFilter) ([]domain.LoanOrder, error)

	// Leases
	CreateLease(ctx context.Context, lease domain.Lease, now time.Time) error
	GetLease(ctx context.Context, id string) (domain.Lease, error)
	GetActiveLeaseForSlot(ctx context.Context, slotID string) (domain.Lease, error)
	NextLeaseEpoch(ctx context.Context, slotID string) (int64, error)
	UpdateLease(ctx context.Context, lease domain.Lease, now time.Time) (domain.Lease, error)
	ListLeases(ctx context.Context, f LeaseFilter) ([]domain.Lease, error)
	ListExpiringLeases(ctx context.Context, now time.Time) ([]domain.Lease, error)

	// Credentials
	CreateCredential(ctx context.Context, cred domain.OneTimeCredential) error
	GetCredentialByHash(ctx context.Context, hash string) (domain.OneTimeCredential, error)
	ConsumeCredential(ctx context.Context, id string, at time.Time) (domain.OneTimeCredential, error)

	// Device commands
	NextSequence(ctx context.Context) (uint32, error)
	CreateDeviceCommand(ctx context.Context, cmd domain.DeviceCommand) error
	GetDeviceCommandBySequence(ctx context.Context, seq uint32) (domain.DeviceCommand, error)
	UpdateDeviceCommand(ctx context.Context, cmd domain.DeviceCommand, now time.Time) (domain.DeviceCommand, error)
	ListCommandsByStatus(ctx context.Context, status domain.DeviceCommandStatus) ([]domain.DeviceCommand, error)
	ListCommandsPastDeadline(ctx context.Context, now time.Time) ([]domain.DeviceCommand, error)

	// Audit
	AppendAudit(ctx context.Context, rec domain.AuditRecord) error
	ListAudit(ctx context.Context, f AuditFilter) ([]domain.AuditRecord, error)
}

// Store is a transactional DB. InTx runs fn against a DB backed by a single
// transaction; if fn returns an error the transaction is rolled back,
// otherwise it is committed. A Store is also a DB for auto-commit use.
type Store interface {
	DB
	InTx(ctx context.Context, fn func(DB) error) error
	Close() error
}

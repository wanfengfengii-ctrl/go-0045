package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"library-locker-lease-controller/internal/domain"
)

// Faults is a mutable, concurrency-safe configuration of injected failures. A
// test flips faults on or off mid-scenario to exercise retry and recovery
// paths. nil hooks mean "no failure".
type Faults struct {
	mu     sync.Mutex
	txFail func() error
	opFail func(op string) error
}

// SetTxFail installs a hook consulted at the start of every InTx call. If it
// returns a non-nil error, the transaction is not started and the error is
// returned wrapped in domain.ErrStorageUnavailable.
func (f *Faults) SetTxFail(fn func() error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txFail = fn
}

// SetOpFail installs a hook consulted before every individual DB operation
// (both auto-commit and inside a transaction). If it returns a non-nil error
// the operation is not performed and the error is returned.
func (f *Faults) SetOpFail(fn func(op string) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opFail = fn
}

func (f *Faults) txErr() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.txFail != nil {
		return f.txFail()
	}
	return nil
}

func (f *Faults) opErr(op string) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opFail != nil {
		return f.opFail(op)
	}
	return nil
}

// NewFaultStore wraps a Store with configurable, controllable failure
// injection. The returned Store is safe for concurrent use.
func NewFaultStore(inner Store, f *Faults) Store {
	return &FaultStore{
		inner:   inner,
		f:       f,
		faultDB: faultDB{inner: inner, f: f},
	}
}

// FaultStore is a Store that wraps another Store with fault injection. It
// embeds faultDB so that direct (auto-commit) DB calls consult the op-fail
// hook; InTx additionally consults the tx-fail hook and wraps the transactional
// DB so that operations inside the transaction are also faulted.
type FaultStore struct {
	inner Store
	f     *Faults
	faultDB
}

func (s *FaultStore) InTx(ctx context.Context, fn func(DB) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrContextCanceled, err)
	}
	if err := s.f.txErr(); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return s.inner.InTx(ctx, func(real DB) error {
		return fn(faultDB{inner: real, f: s.f})
	})
}

func (s *FaultStore) Close() error { return s.inner.Close() }

// faultDB wraps a DB and consults the op-fail hook before every operation.
type faultDB struct {
	inner DB
	f     *Faults
}

func (d faultDB) CreateSlot(ctx context.Context, slot domain.LockerSlot, now time.Time) error {
	if err := d.f.opErr("CreateSlot"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.CreateSlot(ctx, slot, now)
}

func (d faultDB) GetSlot(ctx context.Context, id string) (domain.LockerSlot, error) {
	if err := d.f.opErr("GetSlot"); err != nil {
		return domain.LockerSlot{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.GetSlot(ctx, id)
}

func (d faultDB) ListSlots(ctx context.Context, status string) ([]domain.LockerSlot, error) {
	if err := d.f.opErr("ListSlots"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListSlots(ctx, status)
}

func (d faultDB) FindFreeSlot(ctx context.Context) (domain.LockerSlot, error) {
	if err := d.f.opErr("FindFreeSlot"); err != nil {
		return domain.LockerSlot{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.FindFreeSlot(ctx)
}

func (d faultDB) UpdateSlot(ctx context.Context, slot domain.LockerSlot, now time.Time) (domain.LockerSlot, error) {
	if err := d.f.opErr("UpdateSlot"); err != nil {
		return domain.LockerSlot{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.UpdateSlot(ctx, slot, now)
}

func (d faultDB) CreateOrder(ctx context.Context, order domain.LoanOrder) error {
	if err := d.f.opErr("CreateOrder"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.CreateOrder(ctx, order)
}

func (d faultDB) GetOrder(ctx context.Context, id string) (domain.LoanOrder, error) {
	if err := d.f.opErr("GetOrder"); err != nil {
		return domain.LoanOrder{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.GetOrder(ctx, id)
}

func (d faultDB) UpdateOrderStatus(ctx context.Context, id string, to domain.OrderStatus, now time.Time) error {
	if err := d.f.opErr("UpdateOrderStatus"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.UpdateOrderStatus(ctx, id, to, now)
}

func (d faultDB) ListOrders(ctx context.Context, f OrderFilter) ([]domain.LoanOrder, error) {
	if err := d.f.opErr("ListOrders"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListOrders(ctx, f)
}

func (d faultDB) CreateLease(ctx context.Context, lease domain.Lease, now time.Time) error {
	if err := d.f.opErr("CreateLease"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.CreateLease(ctx, lease, now)
}

func (d faultDB) GetLease(ctx context.Context, id string) (domain.Lease, error) {
	if err := d.f.opErr("GetLease"); err != nil {
		return domain.Lease{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.GetLease(ctx, id)
}

func (d faultDB) GetActiveLeaseForSlot(ctx context.Context, slotID string) (domain.Lease, error) {
	if err := d.f.opErr("GetActiveLeaseForSlot"); err != nil {
		return domain.Lease{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.GetActiveLeaseForSlot(ctx, slotID)
}

func (d faultDB) NextLeaseEpoch(ctx context.Context, slotID string) (int64, error) {
	if err := d.f.opErr("NextLeaseEpoch"); err != nil {
		return 0, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.NextLeaseEpoch(ctx, slotID)
}

func (d faultDB) UpdateLease(ctx context.Context, lease domain.Lease, now time.Time) (domain.Lease, error) {
	if err := d.f.opErr("UpdateLease"); err != nil {
		return domain.Lease{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.UpdateLease(ctx, lease, now)
}

func (d faultDB) ListLeases(ctx context.Context, f LeaseFilter) ([]domain.Lease, error) {
	if err := d.f.opErr("ListLeases"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListLeases(ctx, f)
}

func (d faultDB) ListExpiringLeases(ctx context.Context, now time.Time) ([]domain.Lease, error) {
	if err := d.f.opErr("ListExpiringLeases"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListExpiringLeases(ctx, now)
}

func (d faultDB) CreateCredential(ctx context.Context, cred domain.OneTimeCredential) error {
	if err := d.f.opErr("CreateCredential"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.CreateCredential(ctx, cred)
}

func (d faultDB) GetCredentialByHash(ctx context.Context, hash string) (domain.OneTimeCredential, error) {
	if err := d.f.opErr("GetCredentialByHash"); err != nil {
		return domain.OneTimeCredential{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.GetCredentialByHash(ctx, hash)
}

func (d faultDB) ConsumeCredential(ctx context.Context, id string, at time.Time) (domain.OneTimeCredential, error) {
	if err := d.f.opErr("ConsumeCredential"); err != nil {
		return domain.OneTimeCredential{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ConsumeCredential(ctx, id, at)
}

func (d faultDB) NextSequence(ctx context.Context) (uint32, error) {
	if err := d.f.opErr("NextSequence"); err != nil {
		return 0, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.NextSequence(ctx)
}

func (d faultDB) CreateDeviceCommand(ctx context.Context, cmd domain.DeviceCommand) error {
	if err := d.f.opErr("CreateDeviceCommand"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.CreateDeviceCommand(ctx, cmd)
}

func (d faultDB) GetDeviceCommandBySequence(ctx context.Context, seq uint32) (domain.DeviceCommand, error) {
	if err := d.f.opErr("GetDeviceCommandBySequence"); err != nil {
		return domain.DeviceCommand{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.GetDeviceCommandBySequence(ctx, seq)
}

func (d faultDB) UpdateDeviceCommand(ctx context.Context, cmd domain.DeviceCommand, now time.Time) (domain.DeviceCommand, error) {
	if err := d.f.opErr("UpdateDeviceCommand"); err != nil {
		return domain.DeviceCommand{}, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.UpdateDeviceCommand(ctx, cmd, now)
}

func (d faultDB) ListCommandsByStatus(ctx context.Context, status domain.DeviceCommandStatus) ([]domain.DeviceCommand, error) {
	if err := d.f.opErr("ListCommandsByStatus"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListCommandsByStatus(ctx, status)
}

func (d faultDB) ListCommandsPastDeadline(ctx context.Context, now time.Time) ([]domain.DeviceCommand, error) {
	if err := d.f.opErr("ListCommandsPastDeadline"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListCommandsPastDeadline(ctx, now)
}

func (d faultDB) AppendAudit(ctx context.Context, rec domain.AuditRecord) error {
	if err := d.f.opErr("AppendAudit"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.AppendAudit(ctx, rec)
}

func (d faultDB) ListAudit(ctx context.Context, f AuditFilter) ([]domain.AuditRecord, error) {
	if err := d.f.opErr("ListAudit"); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrStorageUnavailable, err)
	}
	return d.inner.ListAudit(ctx, f)
}

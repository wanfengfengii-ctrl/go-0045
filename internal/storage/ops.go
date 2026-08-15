package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"library-locker-lease-controller/internal/domain"
)

// ---- time helpers ----

func nano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func timeFromNano(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// ---- Slots ----

const (
	createSlotSQL = `INSERT INTO slots(id, version, status, current_lease_id, location, created_at, updated_at)
		VALUES(?, 1, ?, ?, ?, ?, ?)`
	getSlotSQL    = `SELECT id, version, status, current_lease_id, location, created_at, updated_at FROM slots WHERE id = ?`
	listSlotsSQL  = `SELECT id, version, status, current_lease_id, location, created_at, updated_at FROM slots`
	freeSlotSQL   = `SELECT id, version, status, current_lease_id, location, created_at, updated_at FROM slots
		WHERE status = 'active' AND current_lease_id = '' ORDER BY id LIMIT 1`
	updateSlotSQL = `UPDATE slots
		SET version = version + 1, status = ?, current_lease_id = ?, location = ?, updated_at = ?
		WHERE id = ? AND version = ?`
)

func (o dbOps) CreateSlot(ctx context.Context, slot domain.LockerSlot, now time.Time) error {
	_, err := o.e.ExecContext(ctx, createSlotSQL,
		slot.ID, string(slot.Status), slot.CurrentLeaseID, slot.Location,
		nano(now), nano(now))
	return mapErr(err)
}

func scanSlot(row *sql.Row) (domain.LockerSlot, error) {
	var s domain.LockerSlot
	var status, lease, loc string
	var created, updated int64
	err := row.Scan(&s.ID, &s.Version, &status, &lease, &loc, &created, &updated)
	if err != nil {
		return s, mapErr(err)
	}
	s.Status = domain.SlotStatus(status)
	s.CurrentLeaseID = lease
	s.Location = loc
	return s, nil
}

func scanSlots(rows *sql.Rows) ([]domain.LockerSlot, error) {
	defer rows.Close()
	var out []domain.LockerSlot
	for rows.Next() {
		var s domain.LockerSlot
		var status, lease, loc string
		var created, updated int64
		if err := rows.Scan(&s.ID, &s.Version, &status, &lease, &loc, &created, &updated); err != nil {
			return nil, mapErr(err)
		}
		s.Status = domain.SlotStatus(status)
		s.CurrentLeaseID = lease
		s.Location = loc
		out = append(out, s)
	}
	return out, mapErr(rows.Err())
}

func (o dbOps) GetSlot(ctx context.Context, id string) (domain.LockerSlot, error) {
	return scanSlot(o.e.QueryRowContext(ctx, getSlotSQL, id))
}

func (o dbOps) ListSlots(ctx context.Context, status string) ([]domain.LockerSlot, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = o.e.QueryContext(ctx, listSlotsSQL+" ORDER BY id")
	} else {
		rows, err = o.e.QueryContext(ctx, listSlotsSQL+" WHERE status = ? ORDER BY id", status)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return scanSlots(rows)
}

func (o dbOps) FindFreeSlot(ctx context.Context) (domain.LockerSlot, error) {
	return scanSlot(o.e.QueryRowContext(ctx, freeSlotSQL))
}

// UpdateSlot performs a version CAS: it only applies if the stored version
// matches slot.Version. On success it returns the slot with the bumped version.
func (o dbOps) UpdateSlot(ctx context.Context, slot domain.LockerSlot, now time.Time) (domain.LockerSlot, error) {
	res, err := o.e.ExecContext(ctx, updateSlotSQL,
		string(slot.Status), slot.CurrentLeaseID, slot.Location, nano(now),
		slot.ID, slot.Version)
	if err != nil {
		return slot, mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return slot, mapErr(err)
	}
	if n == 0 {
		return slot, fmt.Errorf("%w: slot %s version mismatch", domain.ErrLeaseConflict, slot.ID)
	}
	slot.Version++
	return slot, nil
}

// ---- Orders ----

const (
	createOrderSQL = `INSERT INTO orders(id, type, status, slot_id, reader_id, book_id, lease_id, credential_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	getOrderSQL    = `SELECT id, type, status, slot_id, reader_id, book_id, lease_id, credential_id, created_at, updated_at FROM orders WHERE id = ?`
	updateOrderSQL = `UPDATE orders SET status = ?, updated_at = ? WHERE id = ?`
)

func (o dbOps) CreateOrder(ctx context.Context, order domain.LoanOrder) error {
	_, err := o.e.ExecContext(ctx, createOrderSQL,
		order.ID, string(order.Type), string(order.Status), order.SlotID,
		order.ReaderID, order.BookID, order.LeaseID, order.CredentialID,
		nano(order.CreatedAt), nano(order.UpdatedAt))
	return mapErr(err)
}

func (o dbOps) GetOrder(ctx context.Context, id string) (domain.LoanOrder, error) {
	var order domain.LoanOrder
	var typ, status string
	var created, updated int64
	err := o.e.QueryRowContext(ctx, getOrderSQL, id).Scan(
		&order.ID, &typ, &status, &order.SlotID, &order.ReaderID, &order.BookID,
		&order.LeaseID, &order.CredentialID, &created, &updated)
	if err != nil {
		return order, mapErr(err)
	}
	order.Type = domain.OrderType(typ)
	order.Status = domain.OrderStatus(status)
	order.CreatedAt = timeFromNano(created)
	order.UpdatedAt = timeFromNano(updated)
	return order, nil
}

func (o dbOps) UpdateOrderStatus(ctx context.Context, id string, to domain.OrderStatus, now time.Time) error {
	_, err := o.e.ExecContext(ctx, updateOrderSQL, string(to), now.UnixNano(), id)
	return mapErr(err)
}

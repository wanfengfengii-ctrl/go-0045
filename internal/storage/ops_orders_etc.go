package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"library-locker-lease-controller/internal/domain"
)

func (o dbOps) ListOrders(ctx context.Context, f OrderFilter) ([]domain.LoanOrder, error) {
	var (
		sb   strings.Builder
		args []any
	)
	sb.WriteString(`SELECT id, type, status, slot_id, reader_id, book_id, lease_id, credential_id, created_at, updated_at FROM orders WHERE 1=1`)
	if f.Status != "" {
		sb.WriteString(` AND status = ?`)
		args = append(args, f.Status)
	}
	if f.ReaderID != "" {
		sb.WriteString(` AND reader_id = ?`)
		args = append(args, f.ReaderID)
	}
	if f.SlotID != "" {
		sb.WriteString(` AND slot_id = ?`)
		args = append(args, f.SlotID)
	}
	sb.WriteString(` ORDER BY created_at DESC`)
	if f.Limit > 0 {
		sb.WriteString(` LIMIT ?`)
		args = append(args, f.Limit)
	}
	rows, err := o.e.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.LoanOrder
	for rows.Next() {
		var order domain.LoanOrder
		var typ, status string
		var created, updated int64
		if err := rows.Scan(&order.ID, &typ, &status, &order.SlotID, &order.ReaderID,
			&order.BookID, &order.LeaseID, &order.CredentialID, &created, &updated); err != nil {
			return nil, mapErr(err)
		}
		order.Type = domain.OrderType(typ)
		order.Status = domain.OrderStatus(status)
		order.CreatedAt = timeFromNano(created)
		order.UpdatedAt = timeFromNano(updated)
		out = append(out, order)
	}
	return out, mapErr(rows.Err())
}

// ---- Leases ----

const (
	createLeaseSQL = `INSERT INTO leases(id, slot_id, order_id, epoch, status, version, created_at, expires_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`
	getLeaseSQL          = `SELECT id, slot_id, order_id, epoch, status, version, created_at, expires_at, updated_at FROM leases WHERE id = ?`
	getActiveLeaseSQL    = `SELECT id, slot_id, order_id, epoch, status, version, created_at, expires_at, updated_at FROM leases WHERE slot_id = ? AND status IN ('active','releasing') LIMIT 1`
	nextEpochSQL         = `SELECT COALESCE(MAX(epoch), 0) FROM leases WHERE slot_id = ?`
	updateLeaseSQL       = `UPDATE leases SET status = ?, version = version + 1, updated_at = ? WHERE id = ? AND version = ?`
	listExpiringLeasesSQL = `SELECT id, slot_id, order_id, epoch, status, version, created_at, expires_at, updated_at FROM leases
		WHERE status IN ('active','releasing') AND expires_at != 0 AND expires_at < ? ORDER BY expires_at`
)

func scanLease(row *sql.Row) (domain.Lease, error) {
	var l domain.Lease
	var status string
	var created, expires, updated int64
	err := row.Scan(&l.ID, &l.SlotID, &l.OrderID, &l.Epoch, &status, &l.Version, &created, &expires, &updated)
	if err != nil {
		return l, mapErr(err)
	}
	l.Status = domain.LeaseStatus(status)
	l.CreatedAt = timeFromNano(created)
	l.ExpiresAt = timeFromNano(expires)
	l.UpdatedAt = timeFromNano(updated)
	return l, nil
}

func scanLeases(rows *sql.Rows) ([]domain.Lease, error) {
	defer rows.Close()
	var out []domain.Lease
	for rows.Next() {
		var l domain.Lease
		var status string
		var created, expires, updated int64
		if err := rows.Scan(&l.ID, &l.SlotID, &l.OrderID, &l.Epoch, &status, &l.Version, &created, &expires, &updated); err != nil {
			return nil, mapErr(err)
		}
		l.Status = domain.LeaseStatus(status)
		l.CreatedAt = timeFromNano(created)
		l.ExpiresAt = timeFromNano(expires)
		l.UpdatedAt = timeFromNano(updated)
		out = append(out, l)
	}
	return out, mapErr(rows.Err())
}

func (o dbOps) CreateLease(ctx context.Context, lease domain.Lease, now time.Time) error {
	_, err := o.e.ExecContext(ctx, createLeaseSQL,
		lease.ID, lease.SlotID, lease.OrderID, lease.Epoch, string(lease.Status), lease.Version,
		nano(now), nano(lease.ExpiresAt), nano(now))
	return mapErr(err)
}

func (o dbOps) GetLease(ctx context.Context, id string) (domain.Lease, error) {
	return scanLease(o.e.QueryRowContext(ctx, getLeaseSQL, id))
}

func (o dbOps) GetActiveLeaseForSlot(ctx context.Context, slotID string) (domain.Lease, error) {
	return scanLease(o.e.QueryRowContext(ctx, getActiveLeaseSQL, slotID))
}

func (o dbOps) NextLeaseEpoch(ctx context.Context, slotID string) (int64, error) {
	var maxEpoch int64
	err := o.e.QueryRowContext(ctx, nextEpochSQL, slotID).Scan(&maxEpoch)
	if err != nil {
		return 0, mapErr(err)
	}
	return maxEpoch + 1, nil
}

func (o dbOps) UpdateLease(ctx context.Context, lease domain.Lease, now time.Time) (domain.Lease, error) {
	res, err := o.e.ExecContext(ctx, updateLeaseSQL, string(lease.Status), nano(now), lease.ID, lease.Version)
	if err != nil {
		return lease, mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return lease, mapErr(err)
	}
	if n == 0 {
		return lease, fmt.Errorf("%w: lease %s version mismatch", domain.ErrLeaseConflict, lease.ID)
	}
	lease.Version++
	lease.UpdatedAt = now
	return lease, nil
}

func (o dbOps) ListLeases(ctx context.Context, f LeaseFilter) ([]domain.Lease, error) {
	var (
		sb   strings.Builder
		args []any
	)
	sb.WriteString(`SELECT id, slot_id, order_id, epoch, status, version, created_at, expires_at, updated_at FROM leases WHERE 1=1`)
	if f.Status != "" {
		sb.WriteString(` AND status = ?`)
		args = append(args, f.Status)
	}
	if f.SlotID != "" {
		sb.WriteString(` AND slot_id = ?`)
		args = append(args, f.SlotID)
	}
	sb.WriteString(` ORDER BY created_at DESC`)
	if f.Limit > 0 {
		sb.WriteString(` LIMIT ?`)
		args = append(args, f.Limit)
	}
	rows, err := o.e.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, mapErr(err)
	}
	return scanLeases(rows)
}

func (o dbOps) ListExpiringLeases(ctx context.Context, now time.Time) ([]domain.Lease, error) {
	rows, err := o.e.QueryContext(ctx, listExpiringLeasesSQL, nano(now))
	if err != nil {
		return nil, mapErr(err)
	}
	return scanLeases(rows)
}

// ---- Credentials ----

const (
	createCredentialSQL = `INSERT INTO credentials(id, order_id, slot_id, hash, status, issued_at, expires_at, consumed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, 0)`
	getCredentialSQL    = `SELECT id, order_id, slot_id, hash, status, issued_at, expires_at, consumed_at FROM credentials WHERE hash = ?`
	consumeCredentialSQL = `UPDATE credentials SET status = 'consumed', consumed_at = ? WHERE id = ? AND status = 'issued'`
)

func (o dbOps) CreateCredential(ctx context.Context, cred domain.OneTimeCredential) error {
	_, err := o.e.ExecContext(ctx, createCredentialSQL,
		cred.ID, cred.OrderID, cred.SlotID, cred.Hash, string(cred.Status),
		nano(cred.IssuedAt), nano(cred.ExpiresAt))
	return mapErr(err)
}

func (o dbOps) GetCredentialByHash(ctx context.Context, hash string) (domain.OneTimeCredential, error) {
	var c domain.OneTimeCredential
	var status string
	var issued, expires, consumed int64
	err := o.e.QueryRowContext(ctx, getCredentialSQL, hash).Scan(
		&c.ID, &c.OrderID, &c.SlotID, &c.Hash, &status, &issued, &expires, &consumed)
	if err != nil {
		return c, mapErr(err)
	}
	c.Status = domain.CredentialStatus(status)
	c.IssuedAt = timeFromNano(issued)
	c.ExpiresAt = timeFromNano(expires)
	c.ConsumedAt = timeFromNano(consumed)
	return c, nil
}

// ConsumeCredential atomically flips an issued credential to consumed via a
// status CAS. It returns the updated credential or a domain error distinguishing
// already-used / expired / not-found. The caller is expected to have fetched the
// credential first and validated its status; this method enforces single-use at
// the row level so a concurrent consumer loses the CAS.
func (o dbOps) ConsumeCredential(ctx context.Context, id string, at time.Time) (domain.OneTimeCredential, error) {
	res, err := o.e.ExecContext(ctx, consumeCredentialSQL, nano(at), id)
	if err != nil {
		return domain.OneTimeCredential{}, mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return domain.OneTimeCredential{}, mapErr(err)
	}
	if n == 0 {
		// Either missing or no longer issued; fetch to classify.
		var c domain.OneTimeCredential
		var status string
		var issued, expires, consumed int64
		serr := o.e.QueryRowContext(ctx,
			`SELECT id, order_id, slot_id, hash, status, issued_at, expires_at, consumed_at FROM credentials WHERE id = ?`, id).
			Scan(&c.ID, &c.OrderID, &c.SlotID, &c.Hash, &status, &issued, &expires, &consumed)
		if serr != nil {
			return domain.OneTimeCredential{}, mapErr(serr)
		}
		c.Status = domain.CredentialStatus(status)
		c.IssuedAt = timeFromNano(issued)
		c.ExpiresAt = timeFromNano(expires)
		c.ConsumedAt = timeFromNano(consumed)
		switch c.Status {
		case domain.CredentialConsumed:
			return c, fmt.Errorf("%w: credential %s", domain.ErrCredentialUsed, id)
		case domain.CredentialExpired:
			return c, fmt.Errorf("%w: credential %s", domain.ErrCredentialExpired, id)
		default:
			return c, fmt.Errorf("%w: credential %s in state %s", domain.ErrCredentialMismatch, id, c.Status)
		}
	}
	var c domain.OneTimeCredential
	var status string
	var issued, expires, consumed int64
	err = o.e.QueryRowContext(ctx,
		`SELECT id, order_id, slot_id, hash, status, issued_at, expires_at, consumed_at FROM credentials WHERE id = ?`, id).
		Scan(&c.ID, &c.OrderID, &c.SlotID, &c.Hash, &status, &issued, &expires, &consumed)
	if err != nil {
		return domain.OneTimeCredential{}, mapErr(err)
	}
	c.Status = domain.CredentialStatus(status)
	c.IssuedAt = timeFromNano(issued)
	c.ExpiresAt = timeFromNano(expires)
	c.ConsumedAt = timeFromNano(consumed)
	return c, nil
}

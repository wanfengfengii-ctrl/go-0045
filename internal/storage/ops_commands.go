package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"library-locker-lease-controller/internal/domain"
)

// ---- Device commands ----

const (
	nextSeqSQL = `INSERT INTO seq(name, val) VALUES('cmd', 1)
		ON CONFLICT(name) DO UPDATE SET val = seq.val + 1`
	readSeqSQL = `SELECT val FROM seq WHERE name = 'cmd'`

	createCommandSQL = `INSERT INTO device_commands
		(id, slot_id, lease_id, epoch, sequence, code, status, retries, version, deadline, created_at, sent_at, acknowledged_at, result)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 0, 0, '')`
	getCommandBySeqSQL = `SELECT id, slot_id, lease_id, epoch, sequence, code, status, retries, version, deadline, created_at, sent_at, acknowledged_at, result
		FROM device_commands WHERE sequence = ?`
	updateCommandSQL = `UPDATE device_commands
		SET status = ?, retries = ?, deadline = ?, sent_at = ?, acknowledged_at = ?, result = ?, version = version + 1
		WHERE id = ? AND version = ?`
	listCommandsByStatusSQL = `SELECT id, slot_id, lease_id, epoch, sequence, code, status, retries, version, deadline, created_at, sent_at, acknowledged_at, result
		FROM device_commands WHERE status = ? ORDER BY created_at`
	listCommandsPastDeadlineSQL = `SELECT id, slot_id, lease_id, epoch, sequence, code, status, retries, version, deadline, created_at, sent_at, acknowledged_at, result
		FROM device_commands WHERE status = 'sent' AND deadline != 0 AND deadline < ? ORDER BY deadline`
)

func scanCommand(sc interface {
	Scan(dest ...any) error
}) (domain.DeviceCommand, error) {
	var c domain.DeviceCommand
	var code int
	var status string
	var deadline, created, sent, ack int64
	err := sc.Scan(&c.ID, &c.SlotID, &c.LeaseID, &c.Epoch, &c.Sequence, &code,
		&status, &c.Retries, &c.Version, &deadline, &created, &sent, &ack, &c.Result)
	if err != nil {
		return c, mapErr(err)
	}
	c.Code = domain.CommandCode(code)
	c.Status = domain.DeviceCommandStatus(status)
	c.Deadline = timeFromNano(deadline)
	c.CreatedAt = timeFromNano(created)
	c.SentAt = timeFromNano(sent)
	c.AcknowledgedAt = timeFromNano(ack)
	return c, nil
}

func scanCommands(rows *sql.Rows) ([]domain.DeviceCommand, error) {
	defer rows.Close()
	var out []domain.DeviceCommand
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, mapErr(rows.Err())
}

func (o dbOps) NextSequence(ctx context.Context) (uint32, error) {
	if _, err := o.e.ExecContext(ctx, nextSeqSQL); err != nil {
		return 0, mapErr(err)
	}
	var val int64
	if err := o.e.QueryRowContext(ctx, readSeqSQL).Scan(&val); err != nil {
		return 0, mapErr(err)
	}
	if val <= 0 || val > 0xFFFFFFFF {
		return 0, fmt.Errorf("%w: sequence counter overflow", domain.ErrStorageUnavailable)
	}
	return uint32(val), nil
}

func (o dbOps) CreateDeviceCommand(ctx context.Context, cmd domain.DeviceCommand) error {
	_, err := o.e.ExecContext(ctx, createCommandSQL,
		cmd.ID, cmd.SlotID, cmd.LeaseID, cmd.Epoch, cmd.Sequence, int(cmd.Code),
		string(cmd.Status), cmd.Retries, nano(cmd.Deadline), nano(cmd.CreatedAt))
	return mapErr(err)
}

func (o dbOps) GetDeviceCommandBySequence(ctx context.Context, seq uint32) (domain.DeviceCommand, error) {
	return scanCommand(o.e.QueryRowContext(ctx, getCommandBySeqSQL, seq))
}

func (o dbOps) UpdateDeviceCommand(ctx context.Context, cmd domain.DeviceCommand, now time.Time) (domain.DeviceCommand, error) {
	res, err := o.e.ExecContext(ctx, updateCommandSQL,
		string(cmd.Status), cmd.Retries, nano(cmd.Deadline), nano(cmd.SentAt),
		nano(cmd.AcknowledgedAt), cmd.Result, cmd.ID, cmd.Version)
	if err != nil {
		return cmd, mapErr(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return cmd, mapErr(err)
	}
	if n == 0 {
		return cmd, fmt.Errorf("%w: command %s version mismatch", domain.ErrLeaseConflict, cmd.ID)
	}
	cmd.Version++
	return cmd, nil
}

func (o dbOps) ListCommandsByStatus(ctx context.Context, status domain.DeviceCommandStatus) ([]domain.DeviceCommand, error) {
	rows, err := o.e.QueryContext(ctx, listCommandsByStatusSQL, string(status))
	if err != nil {
		return nil, mapErr(err)
	}
	return scanCommands(rows)
}

func (o dbOps) ListCommandsPastDeadline(ctx context.Context, now time.Time) ([]domain.DeviceCommand, error) {
	rows, err := o.e.QueryContext(ctx, listCommandsPastDeadlineSQL, nano(now))
	if err != nil {
		return nil, mapErr(err)
	}
	return scanCommands(rows)
}

// ---- Audit ----

const (
	appendAuditSQL = `INSERT INTO audit(id, event_type, order_id, lease_id, slot_id, sequence, result, detail, at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`
	listAuditSQL = `SELECT id, event_type, order_id, lease_id, slot_id, sequence, result, detail, at FROM audit`
)

func (o dbOps) AppendAudit(ctx context.Context, rec domain.AuditRecord) error {
	_, err := o.e.ExecContext(ctx, appendAuditSQL,
		rec.ID, string(rec.EventType), rec.OrderID, rec.LeaseID, rec.SlotID,
		rec.Sequence, rec.Result, rec.Detail, nano(rec.At))
	return mapErr(err)
}

func (o dbOps) ListAudit(ctx context.Context, f AuditFilter) ([]domain.AuditRecord, error) {
	var (
		sb   strings.Builder
		args []any
	)
	sb.WriteString(listAuditSQL + ` WHERE 1=1`)
	if f.OrderID != "" {
		sb.WriteString(` AND order_id = ?`)
		args = append(args, f.OrderID)
	}
	if f.SlotID != "" {
		sb.WriteString(` AND slot_id = ?`)
		args = append(args, f.SlotID)
	}
	if f.EventType != "" {
		sb.WriteString(` AND event_type = ?`)
		args = append(args, f.EventType)
	}
	sb.WriteString(` ORDER BY at DESC`)
	if f.Limit > 0 {
		sb.WriteString(` LIMIT ?`)
		args = append(args, f.Limit)
	}
	rows, err := o.e.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.AuditRecord
	for rows.Next() {
		var r domain.AuditRecord
		var et string
		var at int64
		if err := rows.Scan(&r.ID, &et, &r.OrderID, &r.LeaseID, &r.SlotID,
			&r.Sequence, &r.Result, &r.Detail, &at); err != nil {
			return nil, mapErr(err)
		}
		r.EventType = domain.AuditEventType(et)
		r.At = timeFromNano(at)
		out = append(out, r)
	}
	return out, mapErr(rows.Err())
}

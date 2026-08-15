package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"library-locker-lease-controller/internal/domain"
)

func TestStorageContextCausePreserved(t *testing.T) {
	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "storage.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	faultStore := NewFaultStore(store, &Faults{})
	contextCases := []struct {
		name    string
		new     func() (context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "canceled",
			new: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline_exceeded",
			new: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Unix(1, 0))
			},
			wantErr: context.DeadlineExceeded,
		},
	}
	paths := []struct {
		name string
		run  func(context.Context) error
	}{
		{
			name: "sqlite_auto_commit",
			run: func(ctx context.Context) error {
				return store.CreateSlot(ctx, domain.LockerSlot{ID: "slot-1", Status: domain.SlotActive}, time.Now())
			},
		},
		{
			name: "sqlite_in_tx",
			run: func(ctx context.Context) error {
				return store.InTx(ctx, func(DB) error {
					return nil
				})
			},
		},
		{
			name: "sqlite_operation_in_tx",
			run: func(ctx context.Context) error {
				return store.InTx(context.Background(), func(db DB) error {
					return db.CreateSlot(ctx, domain.LockerSlot{ID: "slot-2", Status: domain.SlotActive}, time.Now())
				})
			},
		},
		{
			name: "fault_store_in_tx",
			run: func(ctx context.Context) error {
				return faultStore.InTx(ctx, func(DB) error {
					return nil
				})
			},
		},
	}

	for _, contextCase := range contextCases {
		for _, path := range paths {
			t.Run(contextCase.name+"/"+path.name, func(t *testing.T) {
				ctx, cancel := contextCase.new()
				defer cancel()

				err := path.run(ctx)
				if !errors.Is(err, domain.ErrContextCanceled) {
					t.Fatalf("error %v does not wrap domain.ErrContextCanceled", err)
				}
				if !errors.Is(err, contextCase.wantErr) {
					t.Fatalf("error %v does not preserve %v", err, contextCase.wantErr)
				}
			})
		}
	}
}

package dblock_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func createTestConnection(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	box, err := secretbox.New(secretbox.DevKey())
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(db, box)
	conn, err := st.CreateConnection(context.Background(), store.ConnectionInput{
		Name:         testutil.Unique("lockdb"),
		Host:         "127.0.0.1",
		Port:         5432,
		DatabaseName: "sample_db",
		Username:     "sample_app",
		SSLMode:      "disable",
	}, "sample_app")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.DeleteConnection(context.Background(), conn.ID)
	})
	return conn.ID
}

func TestPostgresLocker_MutualExclusionAndParallelDBs(t *testing.T) {
	db := testutil.OpenDB(t)
	locker := dblock.NewPostgresLocker(db)
	dbA := createTestConnection(t, db)
	dbB := createTestConnection(t, db)
	ctx := context.Background()

	const contenders = 6
	var wg sync.WaitGroup
	leases := make([]*dblock.Lease, contenders)
	errs := make([]error, contenders)
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			leases[idx], errs[idx] = locker.Acquire(ctx, dblock.AcquireRequest{
				DatabaseID: dbA,
				OwnerID:    testutil.Unique("worker"),
				TTL:        10 * time.Second,
			})
		}(i)
	}
	wg.Wait()

	var winner *dblock.Lease
	heldErrs := 0
	for i := range leases {
		if errs[i] == nil && leases[i] != nil {
			if winner != nil {
				t.Fatalf("lebih dari satu worker mendapat lock untuk DB %d", dbA)
			}
			winner = leases[i]
		} else if errors.Is(errs[i], dblock.ErrLockHeld) {
			heldErrs++
		} else {
			t.Fatalf("error tak terduga: %v", errs[i])
		}
	}
	if winner == nil || heldErrs != contenders-1 {
		t.Fatalf("winner=%v heldErrs=%d, want 1 winner & %d ErrLockHeld", winner, heldErrs, contenders-1)
	}

	leaseB, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbB,
		OwnerID:    "worker-b",
		TTL:        10 * time.Second,
	})
	if err != nil {
		t.Fatalf("database berbeda harus bisa dikunci secara parallel: %v", err)
	}
	defer locker.Release(ctx, leaseB)

	if err := locker.Release(ctx, winner); err != nil {
		t.Fatalf("release gagal: %v", err)
	}
	leaseA2, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbA,
		OwnerID:    "worker-next",
		TTL:        10 * time.Second,
	})
	if err != nil {
		t.Fatalf("acquire setelah release gagal: %v", err)
	}
	_ = locker.Release(ctx, leaseA2)
}

func TestPostgresLocker_ExpiredLeaseRecoveryAndFencing(t *testing.T) {
	db := testutil.OpenDB(t)
	locker := dblock.NewPostgresLocker(db)
	dbID := createTestConnection(t, db)
	ctx := context.Background()

	staleLease, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbID,
		OwnerID:    "crashed-worker",
		TTL:        120 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(220 * time.Millisecond)

	expired, err := locker.ListExpiredHeld(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, info := range expired {
		if info.DatabaseID == dbID && info.OwnerID == "crashed-worker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListExpiredHeld tidak melaporkan lock kedaluwarsa untuk DB %d", dbID)
	}

	newLease, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbID,
		OwnerID:    "healthy-worker",
		TTL:        10 * time.Second,
	})
	if err != nil {
		t.Fatalf("reclaim expired lock gagal: %v", err)
	}
	defer locker.Release(ctx, newLease)

	if newLease.FencingVersion <= staleLease.FencingVersion {
		t.Fatalf("fencing_version tidak naik: old=%d new=%d", staleLease.FencingVersion, newLease.FencingVersion)
	}

	if err := locker.Verify(ctx, staleLease); !errors.Is(err, dblock.ErrLeaseLost) {
		t.Fatalf("Verify staleLease = %v, want ErrLeaseLost", err)
	}
	if err := locker.Renew(ctx, staleLease, 10*time.Second); !errors.Is(err, dblock.ErrLeaseLost) {
		t.Fatalf("Renew staleLease = %v, want ErrLeaseLost", err)
	}
	_ = locker.Release(ctx, staleLease)

	if err := locker.Verify(ctx, newLease); err != nil {
		t.Fatalf("Release oleh stale worker tidak boleh menghapus lock milik worker baru: %v", err)
	}
}

func TestPostgresLocker_HeartbeatKeepsAliveAndCancelsOnLoss(t *testing.T) {
	db := testutil.OpenDB(t)
	locker := dblock.NewPostgresLocker(db)
	dbID := createTestConnection(t, db)
	ctx := context.Background()

	lease, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbID,
		OwnerID:    "hb-worker",
		TTL:        300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Release(ctx, lease)

	leaseCtx, stop := dblock.StartHeartbeat(ctx, locker, lease, 300*time.Millisecond, 100*time.Millisecond)
	defer stop()

	time.Sleep(450 * time.Millisecond)
	if leaseCtx.Err() != nil {
		t.Fatalf("leaseCtx batal padahal heartbeat masih aktif: %v", leaseCtx.Err())
	}
	if err := locker.Verify(ctx, lease); err != nil {
		t.Fatalf("lease kedaluwarsa padahal heartbeat aktif: %v", err)
	}

	_, _ = db.Exec(`DELETE FROM platform_db_locks WHERE database_id = $1`, dbID)
	select {
	case <-leaseCtx.Done():
	case <-time.After(1 * time.Second):
		t.Fatal("leaseCtx tidak dibatalkan ketika lock hilang")
	}
}

func TestPostgresLocker_BlockedPartialAndUncertainStates(t *testing.T) {
	db := testutil.OpenDB(t)
	locker := dblock.NewPostgresLocker(db)
	dbID := createTestConnection(t, db)
	ctx := context.Background()

	lease, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbID,
		OwnerID:    "batch-worker",
		TTL:        10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := locker.TransitionToBlocked(ctx, lease, dblock.StateBlockedPartial, 42, "batch 3 failed"); err != nil {
		t.Fatal(err)
	}

	if err := locker.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}

	_, err = locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID: dbID,
		OwnerID:    "next-migration-worker",
		TTL:        10 * time.Second,
	})
	var blockedErr *dblock.BlockedError
	if !errors.As(err, &blockedErr) || blockedErr.State != dblock.StateBlockedPartial || blockedErr.ExecutionID != 42 {
		t.Fatalf("expected BlockedError(blocked_partial, 42), got %v", err)
	}

	recLease, err := locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID:              dbID,
		OwnerID:                 "resume-worker",
		TTL:                     10 * time.Second,
		AllowBlockedExecutionID: 42,
	})
	if err != nil {
		t.Fatalf("resume targeting blocking execution harus diizinkan: %v", err)
	}
	if err := locker.Release(ctx, recLease); err != nil {
		t.Fatal(err)
	}

	info, err := locker.Inspect(ctx, dbID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Locked {
		t.Fatalf("lock harus sudah bersih setelah recovery selesai, got %+v", info)
	}
}

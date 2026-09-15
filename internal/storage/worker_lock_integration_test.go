//go:build integration

package storage

import (
	"context"
	"testing"
)

func TestIntegration_WorkerLockOnePerSchema(t *testing.T) {
	store := isolatedStore(t)
	other := isolatedStore(t)
	ctx := context.Background()

	release, ok, err := store.TryWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.TryWorkerLock(ctx); err != nil || ok {
		t.Fatalf("second holder on the same database must be refused: ok=%v err=%v", ok, err)
	}
	rel2, ok, err := other.TryWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("a different schema is a different database for the lock: ok=%v err=%v", ok, err)
	}
	rel2()

	release()
	release2, ok, err := store.TryWorkerLock(ctx)
	if err != nil || !ok {
		t.Fatalf("after release the lock must be free again: ok=%v err=%v", ok, err)
	}
	release2()
}

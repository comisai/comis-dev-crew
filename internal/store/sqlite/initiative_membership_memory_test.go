package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeMembershipMigrationBoundsLiveHeap(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "membership-heap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2048; index++ {
		initiative := persistenceInitiative(
			fmt.Sprintf("initiative-membership-heap-%04d", index), domain.InitiativeDelivered, int64(index+1),
		)
		initiative.ContractArtifacts = make([]string, 128)
		for artifact := range initiative.ContractArtifacts {
			initiative.ContractArtifacts[artifact] = fmt.Sprintf(
				"artifact-%04d-%03d-%s", index, artifact, strings.Repeat("a", 40),
			)
		}
		if err := insertInitiative(ctx, transaction, initiative); err != nil {
			_ = transaction.Rollback()
			t.Fatalf("insert initiative %d: %v", index, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE initiative_members;
		DELETE FROM schema_migrations WHERE version = 47`); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	stop := make(chan struct{})
	started := make(chan struct{})
	peak := make(chan uint64, 1)
	go func() {
		maximum := baseline.HeapAlloc
		close(started)
		for {
			var current runtime.MemStats
			runtime.ReadMemStats(&current)
			if current.HeapAlloc > maximum {
				maximum = current.HeapAlloc
			}
			select {
			case <-stop:
				peak <- maximum
				return
			default:
			}
			// ReadMemStats stops the world, so an unthrottled sampler starves the
			// migration it is measuring. Sampling every 500us still catches the
			// peak of a streaming migration without dominating its runtime.
			time.Sleep(500 * time.Microsecond)
		}
	}()
	<-started
	err = store.applyInitiativeMembershipMigration(ctx)
	close(stop)
	maximum := <-peak
	if err != nil {
		t.Fatal(err)
	}
	const maximumHeapGrowth = 12 * 1024 * 1024
	if growth := maximum - baseline.HeapAlloc; growth > maximumHeapGrowth {
		t.Fatalf("migration live heap growth = %d bytes, want at most %d", growth, maximumHeapGrowth)
	}
}

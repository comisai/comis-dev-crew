package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRuntimeAttachmentPreparationReplayJoinsPendingRegistration(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "pending-registration-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(coordinator.recoveryReady)
	request := runtimeAttachmentRequest(t, workspace, "task-pending-registration-replay")
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := coordinator.PrepareRuntimeAttachment(ctx, request)
		first <- err
	}()
	waitForRuntimeAttachmentEntry(t, coordinator, request.TaskHandle)
	replayObserved := make(chan struct{}, 1)
	coordinator.runtimeAttachmentReplayObserved = func() { replayObserved <- struct{}{} }
	replay := make(chan error, 1)
	go func() {
		_, err := coordinator.PrepareRuntimeAttachment(context.Background(), request)
		replay <- err
	}()
	select {
	case <-replayObserved:
	case <-time.After(time.Second):
		t.Fatal("preparation replay did not observe pending registration")
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first PrepareRuntimeAttachment() error = %v", err)
	}
	if err := <-replay; !errors.Is(err, context.Canceled) {
		t.Fatalf("replayed PrepareRuntimeAttachment() error = %v", err)
	}
}

func TestRuntimeAttachmentPreparationReplayHonorsCancellationAndShutdown(t *testing.T) {
	for _, test := range []struct {
		name     string
		stopWait func(context.CancelFunc, *runtimeAttachmentCoordinator)
		want     error
	}{
		{name: "caller cancellation", stopWait: func(cancel context.CancelFunc, _ *runtimeAttachmentCoordinator) { cancel() }, want: context.Canceled},
		{name: "coordinator shutdown", stopWait: func(_ context.CancelFunc, coordinator *runtimeAttachmentCoordinator) { close(coordinator.runDone) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := shortTempDir(t)
			workspace := filepath.Join(root, "workspace")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
				RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
				NewCredential:           func() (string, error) { return "cancelled-replay-0123456789abcdef", nil },
				NewAttentionOperationID: runtimeAttentionOperationID,
			})
			if err != nil {
				t.Fatal(err)
			}
			close(coordinator.recoveryReady)
			request := runtimeAttachmentRequest(t, workspace, "task-pending-replay-cancel")
			firstCtx, cancelFirst := context.WithCancel(context.Background())
			first := make(chan error, 1)
			go func() {
				_, err := coordinator.PrepareRuntimeAttachment(firstCtx, request)
				first <- err
			}()
			waitForRuntimeAttachmentEntry(t, coordinator, request.TaskHandle)
			observed := make(chan struct{}, 1)
			coordinator.runtimeAttachmentReplayObserved = func() { observed <- struct{}{} }
			replayCtx, cancelReplay := context.WithCancel(context.Background())
			defer cancelReplay()
			replay := make(chan error, 1)
			go func() {
				_, err := coordinator.PrepareRuntimeAttachment(replayCtx, request)
				replay <- err
			}()
			<-observed
			test.stopWait(cancelReplay, coordinator)
			select {
			case err := <-replay:
				if test.want != nil && !errors.Is(err, test.want) {
					t.Fatalf("PrepareRuntimeAttachment(replay) error = %v", err)
				}
				if test.want == nil && err == nil {
					t.Fatal("PrepareRuntimeAttachment(replay) error = nil, want shutdown")
				}
			case <-time.After(time.Second):
				t.Fatal("preparation replay ignored cancellation or shutdown")
			}
			cancelFirst()
			if err := <-first; err == nil {
				t.Fatal("first PrepareRuntimeAttachment() error = nil")
			}
		})
	}
}

func TestRuntimeAttachmentPendingReleaseHonorsCancellation(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "pending-release-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	close(coordinator.recoveryReady)
	request := runtimeAttachmentRequest(t, workspace, "task-pending-release-cancel")
	prepareCtx, cancelPrepare := context.WithCancel(context.Background())
	prepared := make(chan error, 1)
	go func() {
		_, err := coordinator.PrepareRuntimeAttachment(prepareCtx, request)
		prepared <- err
	}()
	waitForRuntimeAttachmentEntry(t, coordinator, request.TaskHandle)
	observed := make(chan struct{}, 1)
	coordinator.runtimeAttachmentReleaseReplayObserved = func() { observed <- struct{}{} }
	releaseCtx, cancelRelease := context.WithCancel(context.Background())
	released := make(chan error, 1)
	go func() { released <- coordinator.ReleaseRuntimeAttachment(releaseCtx, request.TaskHandle) }()
	<-observed
	cancelRelease()
	select {
	case err := <-released:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReleaseRuntimeAttachment(pending) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending release ignored cancellation")
	}
	cancelPrepare()
	if err := <-prepared; !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareRuntimeAttachment(cancelled) error = %v", err)
	}
}

func TestRuntimeAttachmentReleaseReplayHonorsCancellation(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "release-cancel-0123456789abcdef0", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- coordinator.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-runDone; err != nil {
			t.Errorf("runtime coordinator stop error = %v", err)
		}
	})
	request := runtimeAttachmentRequest(t, workspace, "task-release-replay-cancel")
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	paused := make(chan struct{})
	resume := make(chan struct{})
	coordinator.afterRuntimeAttachmentClose = func() error {
		close(paused)
		<-resume
		return errors.New("simulated release interruption")
	}
	first := make(chan error, 1)
	go func() { first <- coordinator.ReleaseRuntimeAttachment(context.Background(), request.TaskHandle) }()
	<-paused
	observed := make(chan struct{}, 1)
	coordinator.runtimeAttachmentReleaseReplayObserved = func() { observed <- struct{}{} }
	replayCtx, cancelReplay := context.WithCancel(context.Background())
	replay := make(chan error, 1)
	go func() { replay <- coordinator.ReleaseRuntimeAttachment(replayCtx, request.TaskHandle) }()
	<-observed
	cancelReplay()
	select {
	case err := <-replay:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReleaseRuntimeAttachment(replay) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("release replay ignored cancellation")
	}
	close(resume)
	if err := <-first; err == nil {
		t.Fatal("first ReleaseRuntimeAttachment() error = nil")
	}
}

func TestRuntimeAttachmentReleaseReplayJoinsExactRelease(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "joined-release-0123456789abcdef0", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- coordinator.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-runDone; err != nil {
			t.Errorf("runtime coordinator stop error = %v", err)
		}
	})
	request := runtimeAttachmentRequest(t, workspace, "task-release-replay-join")
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	paused := make(chan struct{})
	resume := make(chan struct{})
	crash := errors.New("simulated release interruption")
	coordinator.afterRuntimeAttachmentClose = func() error {
		close(paused)
		<-resume
		return crash
	}
	first := make(chan error, 1)
	go func() { first <- coordinator.ReleaseRuntimeAttachment(context.Background(), request.TaskHandle) }()
	<-paused
	replayObserved := make(chan struct{}, 1)
	coordinator.runtimeAttachmentReleaseReplayObserved = func() { replayObserved <- struct{}{} }
	second := make(chan error, 1)
	go func() { second <- coordinator.ReleaseRuntimeAttachment(context.Background(), request.TaskHandle) }()
	select {
	case <-replayObserved:
		close(resume)
	case err := <-second:
		close(resume)
		<-first
		t.Fatalf("release replay bypassed the active release: %v", err)
	case <-time.After(time.Second):
		close(resume)
		<-first
		t.Fatal("release replay did not join the active release")
	}
	if err := <-first; !errors.Is(err, crash) {
		t.Fatalf("first ReleaseRuntimeAttachment() error = %v", err)
	}
	if err := <-second; !errors.Is(err, crash) {
		t.Fatalf("replayed ReleaseRuntimeAttachment() error = %v", err)
	}
}

func TestRuntimeAttachmentUnprovenReleaseRevokesAndDrainsOnlyAffectedServer(t *testing.T) {
	root := shortTempDir(t)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store := &blockingRuntimeAttachmentStore{
		reportEntered: make(chan struct{}), reportCanceled: make(chan struct{}),
	}
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: store, Clock: time.Now,
		NewCredential:           func() (string, error) { return "unproven-release-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan *reporter.RuntimeServer, 1)
	coordinator.releasedServerStopped = func(server *reporter.RuntimeServer) { stopped <- server }
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- coordinator.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		if err := <-runDone; err != nil {
			t.Errorf("runtime coordinator stop error = %v", err)
		}
	})
	affectedRequest := runtimeAttachmentRequest(t, workspace, "task-unproven-release-drain")
	affected, err := coordinator.PrepareRuntimeAttachment(context.Background(), affectedRequest)
	if err != nil {
		t.Fatal(err)
	}
	siblingRequest := runtimeAttachmentRequest(t, workspace, "task-unproven-release-sibling")
	siblingRequest.OperationID = "operation-unproven-release-sibling"
	sibling, err := coordinator.PrepareRuntimeAttachment(context.Background(), siblingRequest)
	if err != nil {
		t.Fatal(err)
	}
	affectedClient, err := reporter.NewRuntimeClient(affected.SourcePath, affected.RelayIdentity, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	reportDone := make(chan error, 1)
	go func() {
		_, err := affectedClient.Report(context.Background(), domain.WorkerReport{
			SchemaVersion: 1, LocalReportID: "report-unproven-release-drain",
			BriefRevision: affectedRequest.Brief.Revision, BriefRevisionHash: affectedRequest.Brief.RevisionHash,
			Kind: domain.ReportProgress, Summary: "release drain regression", WorkerObservedAt: &observed,
		})
		reportDone <- err
	}()
	select {
	case <-store.reportEntered:
	case <-time.After(time.Second):
		t.Fatal("accepted report did not reach the durable sink")
	}
	recordName, err := runtimeAttachmentIdentityName(affectedRequest.TaskHandle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(coordinator.runtimeRoot, recordName)); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), affectedRequest.TaskHandle); err == nil {
		t.Fatal("ReleaseRuntimeAttachment(unproven identity) error = nil")
	}
	select {
	case <-store.reportCanceled:
	case <-time.After(time.Second):
		t.Fatal("unproven release did not cancel the accepted report")
	}
	if err := <-reportDone; err == nil {
		t.Fatal("accepted report completed after release revocation")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("unproven release did not join the affected server")
	}
	if _, err := affectedClient.Brief(context.Background()); err == nil {
		t.Fatal("released unproven attachment remained reachable")
	}
	siblingClient, err := reporter.NewRuntimeClient(sibling.SourcePath, sibling.RelayIdentity, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := siblingClient.Brief(context.Background()); err != nil {
		t.Fatalf("unaffected sibling brief error = %v", err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), affectedRequest.TaskHandle); err == nil {
		t.Fatal("ReleaseRuntimeAttachment(unproven replay) error = nil")
	}
	if len(store.taskRefusals) != 1 || store.taskRefusals[0].TaskHandle != affectedRequest.TaskHandle {
		t.Fatalf("task refusals = %#v", store.taskRefusals)
	}
	if info, err := os.Lstat(filepath.Dir(affected.SourcePath)); err != nil || !info.IsDir() {
		t.Fatalf("ambiguous task path was not preserved: %#v, %v", info, err)
	}
}

type blockingRuntimeAttachmentStore struct {
	runtimeAttachmentRecoveryStore
	reportEntered  chan struct{}
	reportCanceled chan struct{}
}

func (store *blockingRuntimeAttachmentStore) CommitReport(
	ctx context.Context,
	_ application.ReportMutation,
) (domain.ReportReceipt, error) {
	close(store.reportEntered)
	<-ctx.Done()
	close(store.reportCanceled)
	return domain.ReportReceipt{}, ctx.Err()
}

func waitForRuntimeAttachmentEntry(
	t *testing.T,
	coordinator *runtimeAttachmentCoordinator,
	taskHandle string,
) *runtimeAttachmentEntry {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		entry := coordinator.entries[taskHandle]
		coordinator.mu.Unlock()
		if entry != nil {
			return entry
		}
		runtime.Gosched()
	}
	t.Fatal("runtime attachment entry was not published")
	return nil
}

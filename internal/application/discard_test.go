package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type discardStoreFixture struct {
	*cleanupStoreFixture
	beginDiscardCalls int
}

func (store *discardStoreFixture) BeginTaskDiscard(
	_ context.Context,
	mutation TaskDiscardMutation,
) (TaskCleanupRecord, error) {
	store.beginDiscardCalls++
	record := store.record
	record.OperationID = mutation.OperationID
	record.SubjectDigest = mutation.SubjectDigest
	record.Stage = CleanupPrepared
	record.ReleaseOperationID = mutation.ReleaseOperationID
	record.ReleasedAt = mutation.ReleasedAt
	record.Discard = true
	record.PullRequestID = ""
	record.ReportArtifactHash = ""
	record.HeadRevision = ""
	store.record = record
	return record, nil
}

func discardCoordinator(t *testing.T, dirty bool) (
	*CleanupCoordinator, *discardStoreFixture, *cleanupRemovalFixture, string,
) {
	t.Helper()
	now := time.Date(2026, time.August, 11, 20, 0, 0, 0, time.UTC)
	head := strings.Repeat("b", 40)
	record := cleanupFixtureRecord(head)
	snapshot := cleanupFixtureSnapshot(record, head)
	if dirty {
		snapshot.Cleanliness = WorkspaceDirty
	}
	store := &discardStoreFixture{cleanupStoreFixture: &cleanupStoreFixture{record: record}}
	remover := &cleanupRemovalFixture{}
	coordinator, err := NewCleanupCoordinator(CleanupCoordinatorConfig{
		Store: store, Workspaces: &cleanupWorkspaceFixture{snapshot: snapshot},
		Forge: &cleanupForgeFixture{}, Releaser: &cleanupReleaseFixture{},
		Attachments: &cleanupAttachmentReleaseFixture{}, Remover: remover,
		Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCleanupCoordinator() error = %v", err)
	}
	return coordinator, store, remover, record.TaskHandle
}

// The acknowledgement is the only gate a discard has. Cleanup proves removal is
// safe by pointing at delivered work; a discard has nothing to point at, so an
// unacknowledged one must not reach the durable layer at all.
func TestCleanupCoordinator_DiscardRefusesWithoutAnExplicitAcknowledgement(t *testing.T) {
	coordinator, store, remover, handle := discardCoordinator(t, false)

	_, err := coordinator.DiscardTask(context.Background(), DiscardTaskCommand{
		OperationID: "operation-discard-0001", TaskHandle: handle,
	})

	if err == nil {
		t.Fatal("DiscardTask() without acknowledgement error = nil, want a refusal")
	}
	if store.beginDiscardCalls != 0 || remover.calls != 0 {
		t.Errorf("an unacknowledged discard reached the durable layer: begin=%d remove=%d",
			store.beginDiscardCalls, remover.calls)
	}
	// The refusal must say what the operator has to confirm; a bare precondition
	// leaves them guessing at a flag whose whole purpose is deliberate typing.
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("discard refusal = %v, want a classified failure", err)
	}
	if !strings.Contains(strings.ToLower(failure.Message), "cannot be undone") {
		t.Errorf("discard refusal message = %q", failure.Message)
	}
	if failure.Retryable {
		t.Error("an unacknowledged discard does not become acknowledged by retrying")
	}
}

// A discard exists to remove uncommitted work, so a dirty tree is the ordinary
// case. Re-applying cleanup's cleanliness check here would refuse exactly the
// situation the command was written for.
func TestCleanupCoordinator_DiscardRemovesADirtyWorktreeItWasAskedTo(t *testing.T) {
	coordinator, store, remover, handle := discardCoordinator(t, true)

	result, err := coordinator.DiscardTask(context.Background(), DiscardTaskCommand{
		OperationID: "operation-discard-0001", TaskHandle: handle, Acknowledged: true,
	})

	if err != nil {
		t.Fatalf("DiscardTask() error = %v", err)
	}
	if store.beginDiscardCalls != 1 || remover.calls != 1 {
		t.Fatalf("discard flow: begin=%d remove=%d", store.beginDiscardCalls, remover.calls)
	}
	if result.Task.State != domain.TaskCleaned {
		t.Errorf("discarded task state = %q", result.Task.State)
	}
	// Host authority is released before removal, exactly as cleanup does: the
	// sequence is what must not diverge between two commands that both end in an
	// irreversible deletion.
	if store.releaseCalls != 1 || store.authorizeCalls != 1 {
		t.Errorf("discard skipped a release stage: release=%d authorize=%d",
			store.releaseCalls, store.authorizeCalls)
	}
}

func TestCleanupCoordinator_DiscardRefusesForgedIdentityAndDeadContexts(t *testing.T) {
	coordinator, _, _, handle := discardCoordinator(t, false)
	valid := DiscardTaskCommand{
		OperationID: "operation-discard-0001", TaskHandle: handle, Acknowledged: true,
	}

	for name, mutate := range map[string]func(*DiscardTaskCommand){
		"no operation": func(c *DiscardTaskCommand) { c.OperationID = "" },
		"forged task":  func(c *DiscardTaskCommand) { c.TaskHandle = "../../etc" },
	} {
		command := valid
		mutate(&command)
		if _, err := coordinator.DiscardTask(context.Background(), command); err == nil {
			t.Errorf("%s: expected the discard to be refused", name)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := coordinator.DiscardTask(cancelled, valid); err == nil {
		t.Error("DiscardTask(cancelled) error = nil")
	}
}

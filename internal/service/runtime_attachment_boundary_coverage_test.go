package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestRuntimeAttachmentMountIdentityRejectsUnusableDescriptor(t *testing.T) {
	if _, err := runtimeAttachmentDescriptorMountID(-1); err == nil {
		t.Fatal("mount identity was resolved from an unusable descriptor")
	}
}

func TestTaskRuntimeDirectoryRequiresCanonicalAndReachablePath(t *testing.T) {
	root := shortTempDir(t)
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(real, "task")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	err := ensureTaskRuntimeDirectory(filepath.Join(link, "task"))
	if err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("ensureTaskRuntimeDirectory(uncanonical ancestor) error = %v", err)
	}

	sealed := filepath.Join(root, "sealed")
	if err := os.Mkdir(sealed, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })
	if _, err := ensureOwnedRuntimeRoot(filepath.Join(sealed, "runtime")); err == nil {
		t.Fatal("runtime root was created beneath an unwritable parent")
	}
}

func TestCommitUnverifiedCandidateRejectsUnusableEvidenceAuthority(t *testing.T) {
	supervisor := &candidateSupervisor{config: candidateSupervisorConfig{
		Clock: func() time.Time { return time.Time{} },
	}}
	task := domain.Task{Handle: "task-unverified-0001", BaseRevision: strings.Repeat("b", 40)}
	snapshot := devgit.CandidateSnapshot{HeadRevision: strings.Repeat("c", 40), Cleanliness: devgit.CandidateDirty}
	profile := validation.Profile{EvidenceTTL: time.Hour}
	_, _, err := supervisor.commitUnverifiedCandidate(
		context.Background(), task, profile, snapshot, 0, domain.CandidateValidationDrift,
	)
	if err == nil || !strings.Contains(err.Error(), "evidence time is invalid") {
		t.Fatalf("commitUnverifiedCandidate(zero clock) error = %v", err)
	}

	local := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.Local)
	supervisor = &candidateSupervisor{config: candidateSupervisorConfig{Clock: func() time.Time { return local }}}
	_, _, err = supervisor.commitUnverifiedCandidate(
		context.Background(), task, profile, snapshot, 0, domain.CandidateValidationDrift,
	)
	if err == nil || !strings.Contains(err.Error(), "evidence time is invalid") {
		t.Fatalf("commitUnverifiedCandidate(non-UTC clock) error = %v", err)
	}

	produced := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	supervisor = &candidateSupervisor{config: candidateSupervisorConfig{Clock: func() time.Time { return produced }}}
	_, _, err = supervisor.commitUnverifiedCandidate(
		context.Background(), domain.Task{}, profile, devgit.CandidateSnapshot{}, 0, domain.CandidateValidationDrift,
	)
	if err == nil || !strings.Contains(err.Error(), "could not be sealed") {
		t.Fatalf("commitUnverifiedCandidate(unsealable evidence) error = %v", err)
	}
}

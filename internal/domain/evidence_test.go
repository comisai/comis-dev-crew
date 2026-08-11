package domain

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestCandidateJudge_AcceptsOnlyCurrentShipEvidenceAndForgeTruth(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	evidence := sealCandidateEvidence(t, shipEvidence(task))
	judgment := JudgeCandidate(CandidateJudgeInput{
		Task: task, Evidence: evidence, Now: task.UpdatedAt.Add(5 * time.Minute),
		RequiredLocalChecks: []string{"unit"}, RequiredForgeChecks: []string{"ci/unit"},
	})
	if judgment.Outcome != CandidateAccepted || judgment.Reason != CandidateEvidenceAccepted {
		t.Fatalf("JudgeCandidate() = %#v", judgment)
	}

	tests := []struct {
		name        string
		mutate      func(*DeliveryEvidenceBundle)
		localChecks []string
		forgeChecks []string
		now         time.Time
		wantOutcome CandidateOutcome
		wantReason  CandidateReason
	}{
		{name: "expired", now: task.UpdatedAt.Add(20 * time.Minute), wantOutcome: CandidateUnknown, wantReason: CandidateEvidenceStale},
		{name: "wrong task", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.TaskHandle = "task-other" }, wantOutcome: CandidateUnknown, wantReason: CandidateEvidenceConflicting},
		{name: "wrong repository", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.RepositoryIdentity = "repository-other" }, wantOutcome: CandidateUnknown, wantReason: CandidateEvidenceConflicting},
		{name: "wrong base", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.BaseRevision = strings.Repeat("c", 40) }, wantOutcome: CandidateUnknown, wantReason: CandidateEvidenceConflicting},
		{name: "forge head differs", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ForgeEvidence.HeadRevision = strings.Repeat("c", 40) }, wantOutcome: CandidateUnknown, wantReason: CandidateEvidenceConflicting},
		{name: "dirty worktree", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.WorktreeCleanliness = WorktreeDirty }, wantOutcome: CandidateUnknown, wantReason: CandidateWorktreeUnverified},
		{name: "open decision", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.UnresolvedDecisionCount = 1 }, wantOutcome: CandidateUnknown, wantReason: CandidateDecisionUnresolved},
		{name: "missing local check", localChecks: []string{"unit", "lint"}, wantOutcome: CandidateUnknown, wantReason: CandidateValidationMissing},
		{name: "failed local check", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ValidationReceipts[0].Conclusion = CheckFailed }, wantOutcome: CandidateRejected, wantReason: CandidateValidationFailed},
		{name: "unknown local check", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ValidationReceipts[0].Conclusion = CheckUnknown }, wantOutcome: CandidateUnknown, wantReason: CandidateValidationUnknown},
		{name: "missing forge check", forgeChecks: []string{"ci/unit", "ci/security"}, wantOutcome: CandidateUnknown, wantReason: CandidateForgeMissing},
		{name: "failed forge check", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.ForgeEvidence.CheckConclusions[0].Conclusion = CheckFailed
		}, wantOutcome: CandidateRejected, wantReason: CandidateForgeFailed},
		{name: "pending forge check", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.ForgeEvidence.CheckConclusions[0].Conclusion = CheckPending
		}, wantOutcome: CandidateUnknown, wantReason: CandidateForgeUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := shipEvidence(task)
			if test.mutate != nil {
				test.mutate(&bundle)
			}
			sealed := sealCandidateEvidence(t, bundle)
			localChecks := test.localChecks
			if localChecks == nil {
				localChecks = []string{"unit"}
			}
			forgeChecks := test.forgeChecks
			if forgeChecks == nil {
				forgeChecks = []string{"ci/unit"}
			}
			now := test.now
			if now.IsZero() {
				now = task.UpdatedAt.Add(5 * time.Minute)
			}
			got := JudgeCandidate(CandidateJudgeInput{
				Task: task, Evidence: sealed, Now: now,
				RequiredLocalChecks: localChecks, RequiredForgeChecks: forgeChecks,
			})
			if got.Outcome != test.wantOutcome || got.Reason != test.wantReason {
				t.Fatalf("JudgeCandidate() = %#v", got)
			}
		})
	}
}

func TestCandidateJudge_RequiresImmutableScoutReportArtifact(t *testing.T) {
	task := validTask(ShapeScout, DeliveryReport)
	bundle := shipEvidence(task)
	bundle.ForgeEvidence = nil
	bundle.ReportArtifact = &ReportArtifactEvidence{
		ContentHash: strings.Repeat("e", 64), Size: 128, MediaType: "text/markdown",
	}
	accepted := JudgeCandidate(CandidateJudgeInput{
		Task: task, Evidence: sealCandidateEvidence(t, bundle),
		Now: task.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit"},
	})
	if accepted.Outcome != CandidateAccepted {
		t.Fatalf("JudgeCandidate(scout) = %#v", accepted)
	}
	bundle.ReportArtifact = nil
	missing := JudgeCandidate(CandidateJudgeInput{
		Task: task, Evidence: sealCandidateEvidence(t, bundle),
		Now: task.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit"},
	})
	if missing.Outcome != CandidateUnknown || missing.Reason != CandidateReportMissing {
		t.Fatalf("JudgeCandidate(missing report) = %#v", missing)
	}
}

func TestDeliveryEvidenceSeal_IsCanonicalImmutableAndRejectsAmbiguity(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	bundle := shipEvidence(task)
	sealed, err := SealDeliveryEvidence(bundle)
	if err != nil {
		t.Fatalf("SealDeliveryEvidence() error = %v", err)
	}
	canonical := sealed.Canonical()
	digest := sealed.Digest()
	if len(canonical) == 0 || len(digest) != 64 {
		t.Fatalf("sealed evidence = bytes:%d digest:%q", len(canonical), digest)
	}
	bundle.ValidationReceipts[0].CheckID = "mutated"
	bundle.ForgeEvidence.CheckConclusions[0].Name = "mutated"
	if !bytes.Equal(sealed.Canonical(), canonical) || sealed.Digest() != digest || sealed.Bundle().ValidationReceipts[0].CheckID != "unit" {
		t.Fatal("sealed evidence changed through caller-owned input")
	}
	parsed, err := ParseDeliveryEvidence(canonical, digest)
	if err != nil || parsed.Digest() != digest {
		t.Fatalf("ParseDeliveryEvidence() = %#v, %v", parsed, err)
	}
	changed := append([]byte(nil), canonical...)
	changed[len(changed)-1] ^= 1
	if _, err := ParseDeliveryEvidence(changed, digest); err == nil {
		t.Fatal("ParseDeliveryEvidence(changed) error = nil")
	}

	invalid := shipEvidence(task)
	invalid.ValidationReceipts = append(invalid.ValidationReceipts, invalid.ValidationReceipts[0])
	if _, err := SealDeliveryEvidence(invalid); err == nil {
		t.Fatal("SealDeliveryEvidence(duplicate receipt) error = nil")
	}
	invalid = shipEvidence(task)
	invalid.ExpiresAt = invalid.ProducedAt
	if _, err := SealDeliveryEvidence(invalid); err == nil {
		t.Fatal("SealDeliveryEvidence(invalid expiry) error = nil")
	}
}

func shipEvidence(task Task) DeliveryEvidenceBundle {
	producedAt := task.UpdatedAt.Add(4 * time.Minute)
	headRevision := strings.Repeat("b", 40)
	return DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: headRevision,
		WorktreeCleanliness: WorktreeClean,
		ValidationReceipts: []ValidationEvidenceReceipt{{
			CheckID: "unit", ProgramID: "go-test", HeadRevision: headRevision,
			Conclusion: CheckPassed, Required: true, OutputHash: strings.Repeat("d", 64),
			StartedAt: producedAt.Add(-time.Minute), CompletedAt: producedAt,
		}},
		ForgeEvidence: &ForgeEvidence{
			Repository: task.RepositoryID, PullRequestID: "pull-request-42", HeadRevision: headRevision,
			CheckConclusions: []ForgeCheckEvidence{{Name: "ci/unit", Conclusion: CheckPassed}},
		},
		UnresolvedDecisionCount: 0, ProducedAt: producedAt, ExpiresAt: producedAt.Add(10 * time.Minute),
	}
}

func sealCandidateEvidence(t *testing.T, bundle DeliveryEvidenceBundle) *SealedDeliveryEvidence {
	t.Helper()
	sealed, err := SealDeliveryEvidence(bundle)
	if err != nil {
		t.Fatalf("SealDeliveryEvidence() error = %v", err)
	}
	return sealed
}

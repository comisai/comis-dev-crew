package domain

import (
	"bytes"
	"crypto/sha256"
	"fmt"
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

func TestCandidateJudge_ClassifiesBaseEqualHeadAsUnverifiedForEveryShape(t *testing.T) {
	for _, test := range []struct {
		name        string
		shape       TaskShape
		delivery    DeliveryMode
		forgeChecks []string
	}{
		{name: "ship", shape: ShapeShip, delivery: DeliveryPullRequest, forgeChecks: []string{"ci/unit"}},
		{name: "scout", shape: ShapeScout, delivery: DeliveryReport},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := validTask(test.shape, test.delivery)
			bundle := shipEvidence(task)
			bundle.HeadRevision = task.BaseRevision
			bundle.ForgeEvidence = nil
			for index := range bundle.ValidationReceipts {
				bundle.ValidationReceipts[index].HeadRevision = task.BaseRevision
			}
			judgment := JudgeCandidate(CandidateJudgeInput{
				Task: task, Evidence: sealCandidateEvidence(t, bundle),
				Now: task.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit"},
				RequiredForgeChecks: test.forgeChecks,
			})
			if judgment.Outcome != CandidateUnknown || judgment.Reason != CandidateWorktreeUnverified {
				t.Fatalf("JudgeCandidate(base-equal %s) = %#v", test.name, judgment)
			}
		})
	}
}

func TestCandidateJudge_ClassifiesTaskScopedGitAuthorityDriftAsUnverified(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	for _, reason := range []CandidateUnverifiedReason{
		CandidateReconciliationMismatch,
		CandidateValidationDrift,
	} {
		bundle := shipEvidence(task)
		bundle.UnverifiedReason = reason
		bundle.ValidationReceipts = nil
		bundle.ForgeEvidence = nil
		judgment := JudgeCandidate(CandidateJudgeInput{
			Task: task, Evidence: sealCandidateEvidence(t, bundle),
			Now: task.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit"},
			RequiredForgeChecks: []string{"ci/unit"},
		})
		if judgment.Outcome != CandidateUnknown || judgment.Reason != CandidateWorktreeUnverified {
			t.Fatalf("JudgeCandidate(%s) = %#v", reason, judgment)
		}
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

func TestDeliveryEvidenceSeal_RejectsEveryIncompleteEvidenceClass(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	tests := []struct {
		name   string
		mutate func(*DeliveryEvidenceBundle)
	}{
		{name: "schema", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.SchemaVersion = 2 }},
		{name: "worktree posture", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.WorktreeCleanliness = "forged" }},
		{name: "unverified reason", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.UnverifiedReason = "forged" }},
		{name: "contradictory unverified authority", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.UnverifiedReason = CandidateValidationDrift
		}},
		{name: "missing receipts", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ValidationReceipts = nil }},
		{name: "invalid receipt identity", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ValidationReceipts[0].CheckID = "bad check" }},
		{name: "invalid receipt conclusion", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ValidationReceipts[0].Conclusion = "forged" }},
		{name: "invalid receipt time", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.ValidationReceipts[0].CompletedAt = bundle.ValidationReceipts[0].StartedAt.Add(-time.Second)
		}},
		{name: "ambiguous artifacts", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.ReportArtifact = &ReportArtifactEvidence{ContentHash: strings.Repeat("e", 64), Size: 1, MediaType: "text/plain"}
		}},
		{name: "invalid forge identity", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ForgeEvidence.Repository = "bad repository" }},
		{name: "missing forge checks", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ForgeEvidence.CheckConclusions = nil }},
		{name: "invalid forge check", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.ForgeEvidence.CheckConclusions[0].Name = " bad" }},
		{name: "duplicate forge check", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.ForgeEvidence.CheckConclusions = append(bundle.ForgeEvidence.CheckConclusions, bundle.ForgeEvidence.CheckConclusions[0])
		}},
		{name: "negative decisions", mutate: func(bundle *DeliveryEvidenceBundle) { bundle.UnresolvedDecisionCount = -1 }},
		{name: "non utc production", mutate: func(bundle *DeliveryEvidenceBundle) {
			bundle.ProducedAt = bundle.ProducedAt.In(time.FixedZone("other", 0))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := shipEvidence(task)
			test.mutate(&bundle)
			if _, err := SealDeliveryEvidence(bundle); err == nil {
				t.Fatal("SealDeliveryEvidence() error = nil")
			}
		})
	}
	invalidReport := shipEvidence(validTask(ShapeScout, DeliveryReport))
	invalidReport.ForgeEvidence = nil
	invalidReport.ReportArtifact = &ReportArtifactEvidence{ContentHash: "invalid", Size: 0, MediaType: "text"}
	if _, err := SealDeliveryEvidence(invalidReport); err == nil {
		t.Fatal("SealDeliveryEvidence(invalid report) error = nil")
	}
}

func TestDeliveryEvidenceParse_RejectsMalformedNonCanonicalAndNilAccess(t *testing.T) {
	var nilEvidence *SealedDeliveryEvidence
	if nilEvidence.Digest() != "" || nilEvidence.Canonical() != nil || nilEvidence.Bundle().SchemaVersion != 0 {
		t.Fatal("nil sealed evidence returned content")
	}
	if _, err := ParseDeliveryEvidence(nil, "invalid"); err == nil {
		t.Fatal("ParseDeliveryEvidence(invalid envelope) error = nil")
	}
	for _, content := range [][]byte{
		[]byte(`{"unknown":true}`),
		[]byte(`{} {}`),
		[]byte(" {}"),
	} {
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		if _, err := ParseDeliveryEvidence(content, digest); err == nil {
			t.Fatalf("ParseDeliveryEvidence(%q) error = nil", content)
		}
	}
}

func TestCandidateJudge_RejectsInvalidRequirementsAndMissingDeliveryEvidence(t *testing.T) {
	ship := validTask(ShapeShip, DeliveryPullRequest)
	shipBundle := shipEvidence(ship)
	shipBundle.ForgeEvidence = nil
	tests := []struct {
		name  string
		input CandidateJudgeInput
		want  CandidateReason
	}{
		{name: "nil evidence", input: CandidateJudgeInput{Task: ship, Now: ship.UpdatedAt}, want: CandidateEvidenceInvalid},
		{name: "no local requirements", input: CandidateJudgeInput{Task: ship, Evidence: sealCandidateEvidence(t, shipEvidence(ship)), Now: ship.UpdatedAt.Add(5 * time.Minute), RequiredForgeChecks: []string{"ci/unit"}}, want: CandidateValidationMissing},
		{name: "duplicate local requirements", input: CandidateJudgeInput{Task: ship, Evidence: sealCandidateEvidence(t, shipEvidence(ship)), Now: ship.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit", "unit"}, RequiredForgeChecks: []string{"ci/unit"}}, want: CandidateValidationMissing},
		{name: "missing forge evidence", input: CandidateJudgeInput{Task: ship, Evidence: sealCandidateEvidence(t, shipBundle), Now: ship.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit"}, RequiredForgeChecks: []string{"ci/unit"}}, want: CandidateForgeMissing},
		{name: "duplicate forge requirements", input: CandidateJudgeInput{Task: ship, Evidence: sealCandidateEvidence(t, shipEvidence(ship)), Now: ship.UpdatedAt.Add(5 * time.Minute), RequiredLocalChecks: []string{"unit"}, RequiredForgeChecks: []string{"ci/unit", "ci/unit"}}, want: CandidateForgeMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := JudgeCandidate(test.input)
			if got.Outcome != CandidateUnknown || got.Reason != test.want {
				t.Fatalf("JudgeCandidate() = %#v", got)
			}
		})
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

package domain

import "time"

// CandidateOutcome is the closed domain acceptance result.
type CandidateOutcome string

const (
	CandidateAccepted CandidateOutcome = "accepted"
	CandidateRejected CandidateOutcome = "rejected"
	CandidateUnknown  CandidateOutcome = "unknown"
)

// CandidateReason is a content-free evidence-judgment explanation.
type CandidateReason string

const (
	CandidateEvidenceAccepted    CandidateReason = "evidence_accepted"
	CandidateEvidenceInvalid     CandidateReason = "evidence_invalid"
	CandidateEvidenceStale       CandidateReason = "evidence_stale"
	CandidateEvidenceConflicting CandidateReason = "evidence_conflicting"
	CandidateWorktreeUnverified  CandidateReason = "worktree_unverified"
	CandidateDecisionUnresolved  CandidateReason = "decision_unresolved"
	CandidateValidationMissing   CandidateReason = "validation_missing"
	CandidateValidationFailed    CandidateReason = "validation_failed"
	CandidateValidationUnknown   CandidateReason = "validation_unknown"
	CandidateForgeMissing        CandidateReason = "forge_missing"
	CandidateForgeFailed         CandidateReason = "forge_failed"
	CandidateForgeUnknown        CandidateReason = "forge_unknown"
	CandidateReportMissing       CandidateReason = "report_missing"
)

// CandidateJudgeInput joins a task, sealed facts, and its reviewed requirements.
type CandidateJudgeInput struct {
	Task                Task
	Evidence            *SealedDeliveryEvidence
	RequiredLocalChecks []string
	RequiredForgeChecks []string
	Now                 time.Time
}

// CandidateJudgment is the deterministic domain verdict.
type CandidateJudgment struct {
	Outcome CandidateOutcome
	Reason  CandidateReason
}

// JudgeCandidate accepts only current, matching, complete domain evidence.
func JudgeCandidate(input CandidateJudgeInput) CandidateJudgment {
	if input.Task.Validate() != nil || input.Evidence == nil || input.Now.IsZero() || input.Now.Location() != time.UTC {
		return candidateJudgment(CandidateUnknown, CandidateEvidenceInvalid)
	}
	bundle := input.Evidence.Bundle()
	if bundle.TaskHandle != input.Task.Handle || bundle.RepositoryIdentity != input.Task.RepositoryID ||
		bundle.BaseRevision != input.Task.BaseRevision || bundle.ProducedAt.After(input.Now) {
		return candidateJudgment(CandidateUnknown, CandidateEvidenceConflicting)
	}
	if !input.Now.Before(bundle.ExpiresAt) {
		return candidateJudgment(CandidateUnknown, CandidateEvidenceStale)
	}
	if bundle.WorktreeCleanliness != WorktreeClean {
		return candidateJudgment(CandidateUnknown, CandidateWorktreeUnverified)
	}
	if bundle.HeadRevision == input.Task.BaseRevision {
		return candidateJudgment(CandidateUnknown, CandidateWorktreeUnverified)
	}
	if bundle.UnresolvedDecisionCount != 0 {
		return candidateJudgment(CandidateUnknown, CandidateDecisionUnresolved)
	}
	local := requiredValidationJudgment(bundle, input.RequiredLocalChecks)
	if local != nil {
		return *local
	}
	if input.Task.Shape == ShapeShip {
		return judgeShipCandidate(input, bundle)
	}
	if input.Task.Shape == ShapeScout {
		if bundle.ReportArtifact == nil {
			return candidateJudgment(CandidateUnknown, CandidateReportMissing)
		}
		return candidateJudgment(CandidateAccepted, CandidateEvidenceAccepted)
	}
	return candidateJudgment(CandidateUnknown, CandidateEvidenceInvalid)
}

func requiredValidationJudgment(bundle DeliveryEvidenceBundle, required []string) *CandidateJudgment {
	if len(required) == 0 || hasDuplicateNames(required) {
		judgment := candidateJudgment(CandidateUnknown, CandidateValidationMissing)
		return &judgment
	}
	receipts := make(map[string]ValidationEvidenceReceipt, len(bundle.ValidationReceipts))
	for _, receipt := range bundle.ValidationReceipts {
		receipts[receipt.CheckID] = receipt
	}
	for _, checkID := range required {
		receipt, exists := receipts[checkID]
		if !exists || !receipt.Required || receipt.HeadRevision != bundle.HeadRevision {
			judgment := candidateJudgment(CandidateUnknown, CandidateValidationMissing)
			return &judgment
		}
		switch receipt.Conclusion {
		case CheckPassed:
		case CheckFailed:
			judgment := candidateJudgment(CandidateRejected, CandidateValidationFailed)
			return &judgment
		case CheckPending, CheckUnknown:
			judgment := candidateJudgment(CandidateUnknown, CandidateValidationUnknown)
			return &judgment
		default:
			judgment := candidateJudgment(CandidateUnknown, CandidateEvidenceInvalid)
			return &judgment
		}
	}
	return nil
}

func judgeShipCandidate(input CandidateJudgeInput, bundle DeliveryEvidenceBundle) CandidateJudgment {
	forge := bundle.ForgeEvidence
	if forge == nil || len(input.RequiredForgeChecks) == 0 || hasDuplicateNames(input.RequiredForgeChecks) {
		return candidateJudgment(CandidateUnknown, CandidateForgeMissing)
	}
	if forge.Repository != input.Task.RepositoryID || forge.HeadRevision != bundle.HeadRevision {
		return candidateJudgment(CandidateUnknown, CandidateEvidenceConflicting)
	}
	checks := make(map[string]CheckConclusion, len(forge.CheckConclusions))
	for _, check := range forge.CheckConclusions {
		checks[check.Name] = check.Conclusion
	}
	for _, required := range input.RequiredForgeChecks {
		conclusion, exists := checks[required]
		if !exists {
			return candidateJudgment(CandidateUnknown, CandidateForgeMissing)
		}
		switch conclusion {
		case CheckPassed:
		case CheckFailed:
			return candidateJudgment(CandidateRejected, CandidateForgeFailed)
		case CheckPending, CheckUnknown:
			return candidateJudgment(CandidateUnknown, CandidateForgeUnknown)
		default:
			return candidateJudgment(CandidateUnknown, CandidateEvidenceInvalid)
		}
	}
	return candidateJudgment(CandidateAccepted, CandidateEvidenceAccepted)
}

func hasDuplicateNames(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func candidateJudgment(outcome CandidateOutcome, reason CandidateReason) CandidateJudgment {
	return CandidateJudgment{Outcome: outcome, Reason: reason}
}

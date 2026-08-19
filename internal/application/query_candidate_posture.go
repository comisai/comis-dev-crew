package application

import "github.com/comisai/comis-dev-crew/internal/domain"

// candidatePosture is one operator-facing reading of a durable candidate
// judgment. It is content-free by construction: every field below is fixed
// prose chosen from the closed judgment vocabulary, so no check name, branch,
// path or report body can reach a caller through this read.
type candidatePosture struct {
	reason      string
	explanation string
	rootCause   string
	actions     []NextAction
}

// candidatePostures maps the closed candidate-judgment vocabulary onto operator
// readings. Every reason the judge can return is present: a candidate that
// stalls is precisely when an operator needs the verdict named, and a reason
// absent here would fall back to generic lifecycle text that says no blocking
// reason is recorded while one sits in durable evidence.
var candidatePostures = map[domain.CandidateReason]candidatePosture{
	domain.CandidateEvidenceInvalid: {
		reason:      "candidate_evidence_invalid",
		explanation: "Candidate evidence cannot be read as a valid bundle.",
		rootCause:   "The sealed evidence failed domain validation, so no acceptance verdict can rest on it.",
	},
	domain.CandidateEvidenceStale: {
		reason:      "candidate_evidence_stale",
		explanation: "Candidate evidence expired before it could be accepted.",
		rootCause:   "The evidence lifetime recorded in the bundle has elapsed; a fresh validation pass is required.",
	},
	domain.CandidateEvidenceConflicting: {
		reason:      "candidate_evidence_conflicting",
		explanation: "Candidate evidence does not match the durable task authority.",
		rootCause:   "The sealed bundle names a different task, repository or base revision than the task record.",
	},
	domain.CandidateDecisionUnresolved: {
		reason:      "candidate_decision_unresolved",
		explanation: "Candidate acceptance is waiting on unresolved human decisions.",
		rootCause:   "The evidence records open decisions, and acceptance requires every one of them to be closed first.",
		actions:     []NextAction{ActionInspectTask, ActionResolveBlock},
	},
	domain.CandidateValidationMissing: {
		reason:      "candidate_validation_missing",
		explanation: "Required local validation receipts are absent from candidate evidence.",
		rootCause:   "At least one reviewed required local check has no receipt recorded at the candidate head.",
	},
	domain.CandidateValidationUnknown: {
		reason:      "candidate_validation_unknown",
		explanation: "Required local validation has not concluded.",
		rootCause:   "At least one required local check is still pending or reported an unknown outcome.",
	},
	domain.CandidateValidationFailed: {
		reason:      "candidate_validation_failed",
		explanation: "Candidate evidence was rejected by local validation.",
		rootCause:   "At least one required local validation check failed.",
	},
	domain.CandidateForgeMissing: {
		reason:      "candidate_forge_missing",
		explanation: "Required forge evidence is absent from candidate evidence.",
		rootCause:   "The pull request evidence or a required forge check conclusion is not recorded at the candidate head.",
	},
	domain.CandidateForgeUnknown: {
		reason:      "candidate_forge_unknown",
		explanation: "Required forge checks have not concluded.",
		rootCause:   "At least one required forge check is still pending or reported an unknown conclusion.",
	},
	domain.CandidateForgeFailed: {
		reason:      "candidate_forge_failed",
		explanation: "Candidate evidence was rejected by forge validation.",
		rootCause:   "At least one required forge check failed.",
	},
	domain.CandidateReportMissing: {
		reason:      "candidate_report_missing",
		explanation: "The scout report artifact is absent from candidate evidence.",
		rootCause:   "Acceptance requires a bounded report artifact, and the bundle records none.",
	},
}

// unrecognizedCandidatePosture is what an unmapped judgment reads as. It is
// deliberately loud rather than a silent fall-through to generic lifecycle
// text: a reason this build cannot interpret is a gap an operator must see,
// not a task with nothing wrong.
var unrecognizedCandidatePosture = candidatePosture{
	reason:      "candidate_posture_unrecognized",
	explanation: "A durable candidate judgment was recorded that this build cannot interpret.",
	rootCause:   "The stored judgment reason is outside the vocabulary this service knows.",
	actions:     []NextAction{ActionInspectHealth, ActionInspectTask},
}

// readCandidatePosture resolves one judgment, reporting whether it blocks.
// An accepted verdict blocks nothing, so it leaves the lifecycle text in place.
func readCandidatePosture(judgment domain.CandidateJudgment) (candidatePosture, bool) {
	if judgment.Reason == domain.CandidateEvidenceAccepted {
		return candidatePosture{}, false
	}
	posture, known := candidatePostures[judgment.Reason]
	if !known {
		return unrecognizedCandidatePosture, true
	}
	if len(posture.actions) == 0 {
		posture.actions = []NextAction{ActionInspectTask}
	}
	return posture, true
}

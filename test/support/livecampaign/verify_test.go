package livecampaign

import (
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func acceptedTask(expectation TaskExpectation) application.TaskDetail {
	return application.TaskDetail{
		SchemaVersion: 1, Completeness: application.CompletenessComplete,
		Summary: application.TaskSummary{
			TaskHandle: expectation.TaskHandle, State: domain.TaskCleaned,
			WorkerProfileID: expectation.WorkerProfileID, RepositoryID: "comis-repository",
			Head: strings.Repeat("a", 40), StateSource: application.StateSourceStore,
			StateConfidence: application.ConfidenceVerified, Freshness: application.FreshnessCurrent,
		},
		Evidence: application.TaskEvidenceView{
			Candidate: application.CandidateEvidenceView{
				Status: application.CandidateEvidenceJudged, HeadRevision: strings.Repeat("a", 40), EvidenceDigest: strings.Repeat("b", 64),
				ReconciliationOperationID: func() string {
					if expectation.ExpectReconciliation {
						return "operation-reconcile-e0"
					}
					return ""
				}(),
			},
			Validation: application.ValidationEvidenceView{Status: application.ValidationEvidenceAccepted, EvidenceDigest: strings.Repeat("b", 64)},
			Delivery: application.DeliveryEvidenceView{
				Status: application.DeliveryEvidenceDelivered, EvidenceOperationID: "operation-delivery-e0",
				EvidenceRef: "evidence-delivery-e0", PullRequestID: "github-pr-17",
			},
			Cleanup:   application.CleanupEvidenceView{Status: application.CleanupEvidenceCompleted, OperationID: "operation-cleanup-codex"},
			Authority: application.TaskAuthorityView{ManagedRunID: expectation.ManagedRunID},
		},
		Shape: domain.ShapeShip, BaseRevision: strings.Repeat("d", 40),
		ValidationProfile: "comis-validate", DeliveryMode: domain.DeliveryPullRequest,
	}
}

func completeMessages(manifest Manifest) MessageReport {
	messages := make([]ChannelMessage, 0, len(manifest.Telegram.Checkpoints))
	for index, checkpoint := range manifest.Telegram.Checkpoints {
		messageID := "message-" + checkpoint.Kind
		messages = append(messages, ChannelMessage{
			MessageID: &messageID, EpochMs: manifest.StartedAtMs + int64(index+1)*1000,
			ChannelType: "telegram", SenderID: manifest.Telegram.SenderID,
			Text: "human checkpoint " + checkpoint.Marker, AgentID: manifest.Comis.AgentID,
			ChatID: checkpoint.ChatID, SessionKey: "session-" + checkpoint.ChatID, Origin: "user",
		})
	}
	return MessageReport{
		Schema: "comis-offline-channel-messages-report", SchemaVersion: 2,
		Messages: messages, Completeness: MessageCompleteness{Complete: true},
	}
}

func TestTaskEvidenceRequiresExactCleanedDeliveryPosture(t *testing.T) {
	manifest := validManifest()
	expectation := manifest.Tasks[0]
	detail := acceptedTask(expectation)
	if err := VerifyTask(manifest, expectation, detail); err != nil {
		t.Fatalf("verify accepted task: %v", err)
	}
	for name, mutate := range map[string]func(*application.TaskDetail){
		"not cleaned": func(value *application.TaskDetail) { value.Summary.State = domain.TaskDelivered },
		"missing candidate origin": func(value *application.TaskDetail) {
			value.Evidence.Candidate.ReconciliationOperationID = ""
		},
		"validation absent": func(value *application.TaskDetail) {
			value.Evidence.Validation.Status = application.ValidationEvidenceNotStarted
		},
		"delivery pending": func(value *application.TaskDetail) {
			value.Evidence.Delivery.Status = application.DeliveryEvidencePending
		},
		"cleanup held": func(value *application.TaskDetail) { value.Evidence.Cleanup.Status = application.CleanupEvidenceHeld },
	} {
		t.Run(name, func(t *testing.T) {
			altered := acceptedTask(expectation)
			mutate(&altered)
			if err := VerifyTask(manifest, expectation, altered); err == nil {
				t.Fatal("expected task evidence refusal")
			}
		})
	}
}

func TestOperationEvidenceRequiresCompletedNamedCommand(t *testing.T) {
	expectation := validManifest().Operations[0]
	view := application.OperationView{
		SchemaVersion: 1, OperationID: expectation.OperationID, Command: expectation.Command,
		SubjectDigest: strings.Repeat("d", 64), Status: domain.OperationCompleted,
		StateVersion: 9, CreatedAtMs: 1000, UpdatedAtMs: 2000,
	}
	if err := VerifyOperation(expectation, view); err != nil {
		t.Fatalf("verify completed operation: %v", err)
	}
	view.Command = "CleanupTask"
	if err := VerifyOperation(expectation, view); err == nil {
		t.Fatal("expected altered command refusal")
	}
}

func TestTelegramEvidenceRequiresCompleteHumanCheckpointSequence(t *testing.T) {
	manifest := validManifest()
	report := completeMessages(manifest)
	evidence, err := VerifyMessages(manifest, report)
	if err != nil {
		t.Fatalf("verify complete messages: %v", err)
	}
	if len(evidence) != len(manifest.Telegram.Checkpoints) {
		t.Fatalf("expected %d checkpoint rows, got %d", len(manifest.Telegram.Checkpoints), len(evidence))
	}

	report = completeMessages(manifest)
	report.Completeness.Complete = false
	report.Completeness.Reasons = []string{"source_truncated"}
	if _, err := VerifyMessages(manifest, report); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected incomplete-source refusal, got %v", err)
	}

	report = completeMessages(manifest)
	report.Messages[0].SenderID = "bot"
	if _, err := VerifyMessages(manifest, report); err == nil || !strings.Contains(err.Error(), "human sender") {
		t.Fatalf("expected bot-sender refusal, got %v", err)
	}
}

func TestTelegramEvidenceRejectsOutOfOrderCampaignMilestones(t *testing.T) {
	manifest := validManifest()
	report := completeMessages(manifest)
	indexes := make(map[string]int, len(manifest.Telegram.Checkpoints))
	for index, checkpoint := range manifest.Telegram.Checkpoints {
		indexes[checkpoint.Kind] = index
	}
	mcp := indexes["mcp_restarted_ack"]
	decision := indexes["decision_reply"]
	report.Messages[mcp].EpochMs, report.Messages[decision].EpochMs =
		report.Messages[decision].EpochMs, report.Messages[mcp].EpochMs
	if _, err := VerifyMessages(manifest, report); err == nil ||
		!strings.Contains(err.Error(), "mcp_restarted_ack must precede decision_reply") {
		t.Fatalf("VerifyMessages(out of order) error = %v", err)
	}
}

func TestGitHubEvidenceRequiresCurrentOpenUnmergedHeadAndChecks(t *testing.T) {
	manifest := validManifest()
	detail := acceptedTask(manifest.Tasks[0])
	pull := GitHubPull{Number: 17, State: "open"}
	pull.HTMLURL = "https://github.com/comisai/comis/pull/17"
	pull.Head.SHA = detail.Evidence.Candidate.HeadRevision
	pull.Head.Ref = "devcrew/task-codex-e0"
	pull.Base.Ref = manifest.GitHub.BaseBranch
	success := "success"
	checks := GitHubChecks{Runs: []GitHubCheckRun{{Name: "validate", Status: "completed", Conclusion: &success}}}
	if err := VerifyPullRequest(manifest.GitHub, detail, pull, checks); err != nil {
		t.Fatalf("verify current pull request: %v", err)
	}
	pull.Merged = true
	if err := VerifyPullRequest(manifest.GitHub, detail, pull, checks); err == nil {
		t.Fatal("expected merged pull-request refusal")
	}
}

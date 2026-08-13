package livecampaign

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// MessageReport is the bounded subset consumed from `comis messages --format json`.
type MessageReport struct {
	Schema        string              `json:"schema"`
	SchemaVersion int                 `json:"schemaVersion"`
	Messages      []ChannelMessage    `json:"messages"`
	Completeness  MessageCompleteness `json:"completeness"`
}

type ChannelMessage struct {
	MessageID   *string `json:"messageId"`
	EpochMs     int64   `json:"epochMs"`
	ChannelType string  `json:"channelType"`
	SenderID    string  `json:"senderId"`
	Text        string  `json:"text"`
	AgentID     string  `json:"agentId"`
	ChatID      string  `json:"chatId"`
	SessionKey  string  `json:"sessionKey"`
	Origin      string  `json:"origin"`
}

type MessageCompleteness struct {
	Complete bool     `json:"complete"`
	Reasons  []string `json:"reasons"`
}

type CheckpointEvidence struct {
	Kind       string `json:"kind"`
	ChatID     string `json:"chatId"`
	SenderID   string `json:"senderId"`
	EpochMs    int64  `json:"epochMs"`
	MessageID  string `json:"messageId"`
	SessionKey string `json:"sessionKey"`
}

type GitHubPull struct {
	Number  int    `json:"number"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type GitHubChecks struct {
	Runs []GitHubCheckRun `json:"check_runs"`
}

type GitHubCheckRun struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

func VerifyTask(manifest Manifest, expectation TaskExpectation, detail application.TaskDetail) error {
	if detail.SchemaVersion != 1 || detail.Completeness != application.CompletenessComplete {
		return errors.New("task detail is incomplete or has an unsupported schema")
	}
	if detail.Summary.TaskHandle != expectation.TaskHandle || detail.Summary.RepositoryID != manifest.DevCrew.RepositoryID ||
		detail.Summary.WorkerProfileID != expectation.WorkerProfileID {
		return errors.New("task identity differs from the protected manifest")
	}
	if detail.Summary.State != domain.TaskCleaned || detail.Shape != domain.ShapeShip || detail.DeliveryMode != domain.DeliveryPullRequest {
		return errors.New("task did not finish as one cleaned pull-request ship task")
	}
	if detail.Summary.StateSource != application.StateSourceStore ||
		detail.Summary.StateConfidence != application.ConfidenceVerified || detail.Summary.Freshness != application.FreshnessCurrent {
		return errors.New("task summary is not a current verified durable projection")
	}
	candidate := detail.Evidence.Candidate
	if candidate.Status != application.CandidateEvidenceJudged ||
		!revisionPattern.MatchString(candidate.HeadRevision) || candidate.HeadRevision != detail.Summary.Head ||
		!digestPattern.MatchString(candidate.EvidenceDigest) {
		return errors.New("task candidate evidence is not one exact judged head")
	}
	reconciliationOperationID := expectedOperationID(manifest, expectation.TaskHandle, "ReconcileTask")
	if expectation.ExpectReconciliation {
		if candidate.ReconciliationOperationID == "" || candidate.ReconciliationOperationID != reconciliationOperationID {
			return errors.New("recovered task is missing its durable reconciliation origin")
		}
	} else if candidate.ReconciliationOperationID != "" {
		return errors.New("worker-reported task unexpectedly claims reconciliation origin")
	}
	if detail.Evidence.Validation.Status != application.ValidationEvidenceAccepted ||
		detail.Evidence.Validation.EvidenceDigest != candidate.EvidenceDigest {
		return errors.New("task validation is not accepted for the exact candidate evidence")
	}
	if detail.Evidence.Delivery.Status != application.DeliveryEvidenceDelivered ||
		detail.Evidence.Delivery.EvidenceOperationID == "" || detail.Evidence.Delivery.EvidenceRef == "" ||
		!pullRequestIDPattern.MatchString(detail.Evidence.Delivery.PullRequestID) {
		return errors.New("task delivery lacks one durable pull-request result")
	}
	expectedCleanupOperationID := expectedOperationID(manifest, expectation.TaskHandle, "CleanupTask")
	if detail.Evidence.Cleanup.Status != application.CleanupEvidenceCompleted ||
		detail.Evidence.Cleanup.OperationID != expectedCleanupOperationID || detail.Evidence.Cleanup.OpenHoldCount != 0 {
		return errors.New("task cleanup is not durably complete without holds")
	}
	if detail.Evidence.Authority.ManagedRunID != "" && detail.Evidence.Authority.ManagedRunID != expectation.ManagedRunID {
		return errors.New("task managed-run authority differs from the protected manifest")
	}
	if !revisionPattern.MatchString(detail.BaseRevision) || detail.ValidationProfile == "" {
		return errors.New("task base or validation profile is unavailable")
	}
	return nil
}

func VerifyOperation(expectation OperationExpectation, view application.OperationView) error {
	if view.SchemaVersion != 1 || view.OperationID != expectation.OperationID || view.Command != expectation.Command {
		return errors.New("operation identity or command differs from the protected manifest")
	}
	if view.Status != domain.OperationCompleted || view.ErrorCode != "" {
		return errors.New("operation is not durably completed")
	}
	if view.SubjectDigest == "" || view.StateVersion <= 0 || view.CreatedAtMs <= 0 || view.UpdatedAtMs < view.CreatedAtMs {
		return errors.New("operation evidence is incomplete")
	}
	return nil
}

func VerifyMessages(manifest Manifest, report MessageReport) ([]CheckpointEvidence, error) {
	if report.Schema != "comis-offline-channel-messages-report" || report.SchemaVersion != 2 {
		return nil, errors.New("Telegram message report has an unsupported schema")
	}
	if !report.Completeness.Complete || len(report.Completeness.Reasons) != 0 {
		return nil, errors.New("Telegram message evidence is incomplete")
	}
	evidenceByKind := make(map[string]CheckpointEvidence, len(manifest.Telegram.Checkpoints))
	for _, checkpoint := range manifest.Telegram.Checkpoints {
		matches := make([]ChannelMessage, 0, 1)
		for _, message := range report.Messages {
			if strings.Contains(message.Text, checkpoint.Marker) {
				matches = append(matches, message)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("Telegram checkpoint %s must occur in exactly one message", checkpoint.Kind)
		}
		message := matches[0]
		if message.ChannelType != "telegram" || message.Origin != "user" || message.SenderID != manifest.Telegram.SenderID {
			return nil, fmt.Errorf("Telegram checkpoint %s is not from the required human sender", checkpoint.Kind)
		}
		if message.ChatID != checkpoint.ChatID || message.AgentID != manifest.Comis.AgentID || message.SessionKey == "" {
			return nil, fmt.Errorf("Telegram checkpoint %s is bound to the wrong origin", checkpoint.Kind)
		}
		if message.EpochMs < manifest.StartedAtMs || message.EpochMs > manifest.EndedAtMs {
			return nil, fmt.Errorf("Telegram checkpoint %s is outside the campaign window", checkpoint.Kind)
		}
		messageID := ""
		if message.MessageID != nil {
			messageID = *message.MessageID
		}
		evidenceByKind[checkpoint.Kind] = CheckpointEvidence{
			Kind: checkpoint.Kind, ChatID: message.ChatID, SenderID: message.SenderID,
			EpochMs: message.EpochMs, MessageID: messageID, SessionKey: message.SessionKey,
		}
	}
	if err := verifyCheckpointOrder(evidenceByKind); err != nil {
		return nil, err
	}
	evidence := make([]CheckpointEvidence, 0, len(requiredCheckpointKinds))
	for _, kind := range requiredCheckpointKinds {
		evidence = append(evidence, evidenceByKind[kind])
	}
	return evidence, nil
}

func VerifyPullRequest(target GitHubTarget, detail application.TaskDetail, pull GitHubPull, checks GitHubChecks) error {
	expectedNumber, err := strconv.Atoi(strings.TrimPrefix(detail.Evidence.Delivery.PullRequestID, "github-pr-"))
	if err != nil || pull.Number != expectedNumber {
		return errors.New("GitHub pull-request number differs from durable delivery evidence")
	}
	if pull.State != "open" || pull.Merged || pull.Head.SHA != detail.Evidence.Candidate.HeadRevision ||
		pull.Base.Ref != target.BaseBranch || !strings.HasPrefix(pull.Head.Ref, "devcrew/") {
		return errors.New("GitHub pull request is merged, closed, or bound to stale head/base truth")
	}
	pullURL, err := url.Parse(pull.HTMLURL)
	if err != nil || pullURL.Scheme != "https" || pullURL.Host != "github.com" ||
		pullURL.Path != "/"+target.Repository+"/pull/"+strconv.Itoa(pull.Number) || pullURL.RawQuery != "" || pullURL.Fragment != "" {
		return errors.New("GitHub pull-request URL differs from the protected repository")
	}
	for _, required := range target.RequiredChecks {
		matches := 0
		for _, check := range checks.Runs {
			if check.Name != required {
				continue
			}
			matches++
			if check.Status != "completed" || check.Conclusion == nil || *check.Conclusion != "success" {
				return fmt.Errorf("required GitHub check %s is not currently successful", required)
			}
		}
		if matches != 1 {
			return fmt.Errorf("required GitHub check %s must have exactly one current result", required)
		}
	}
	return nil
}

var (
	revisionPattern      = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	digestPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
	pullRequestIDPattern = regexp.MustCompile(`^github-pr-[1-9][0-9]{0,9}$`)
)

func expectedOperationID(manifest Manifest, taskHandle, command string) string {
	for _, operation := range manifest.Operations {
		if operation.TaskHandle == taskHandle && operation.Command == command {
			return operation.OperationID
		}
	}
	return ""
}

func verifyCheckpointOrder(evidence map[string]CheckpointEvidence) error {
	before := func(first, second string) bool {
		return evidence[first].EpochMs < evidence[second].EpochMs
	}
	for _, pair := range [][2]string{
		{"task_request", "unrelated_conversation"},
		{"task_request", "mcp_restarted_ack"},
		{"unrelated_conversation", "decision_reply"},
		{"unrelated_conversation", "pause_handback"},
		{"unrelated_conversation", "reconcile_approval"},
		{"devcrew_restart_ready", "devcrew_restarted_ack"},
		{"comis_restart_ready", "comis_restarted_ack"},
		{"devcrew_restarted_ack", "cleanup_confirmation"},
		{"comis_restarted_ack", "cleanup_confirmation"},
	} {
		if !before(pair[0], pair[1]) {
			return fmt.Errorf("Telegram checkpoint %s must precede %s", pair[0], pair[1])
		}
	}
	return nil
}

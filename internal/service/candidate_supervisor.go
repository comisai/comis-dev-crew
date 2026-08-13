package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/delivery"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/forge"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

type candidateEvidenceStore interface {
	ListTasks(context.Context) ([]domain.Task, error)
	GetTask(context.Context, string) (domain.Task, error)
	GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error)
	ListAcceptedReports(context.Context, string) ([]domain.AcceptedReport, error)
	CommitCandidateEvidence(context.Context, string, *domain.SealedDeliveryEvidence, []string, []string, time.Time, []application.ComisEvidencePublication) (domain.Task, domain.CandidateJudgment, error)
}

type candidateGitInspector interface {
	InspectCandidate(context.Context, devgit.CandidateSnapshotRequest) (devgit.CandidateSnapshot, error)
}

type candidateValidationRunner interface {
	Run(context.Context, validation.RunRequest) (validation.Receipt, error)
}

type candidatePullRequestDeliverer interface {
	DeliverPullRequest(context.Context, forge.PullRequestRequest) (forge.PullRequestTruth, error)
}

type candidateArtifactInspector func(context.Context, string, int64, string) (delivery.InspectedReportArtifact, error)

type candidateDeliveryMaterial struct {
	referenceURL string
	artifact     *delivery.InspectedReportArtifact
	fileName     string
}

type candidateSupervisorConfig struct {
	Store                    candidateEvidenceStore
	Git                      candidateGitInspector
	Catalog                  *validation.Catalog
	Runner                   candidateValidationRunner
	PullRequests             candidatePullRequestDeliverer
	InspectArtifact          candidateArtifactInspector
	NewValidationOperationID func() (string, error)
	Clock                    application.Clock
	PollInterval             time.Duration
}

type candidateSupervisor struct {
	config candidateSupervisorConfig
}

var errCandidatePullRequestTruthUnavailable = errors.New("validate task candidate: pull-request truth is unavailable")

func newCandidateSupervisor(config candidateSupervisorConfig) (*candidateSupervisor, error) {
	if config.Store == nil || config.Git == nil || config.Catalog == nil || config.Runner == nil ||
		config.PullRequests == nil || config.InspectArtifact == nil || config.Clock == nil ||
		config.NewValidationOperationID == nil ||
		config.PollInterval <= 0 || config.PollInterval > time.Minute {
		return nil, errors.New("create candidate supervisor: evidence dependencies are required")
	}
	return &candidateSupervisor{config: config}, nil
}

// Run recovers the durable validating-task queue. Unknown evidence and
// temporarily unavailable forge truth remain validating and are retried. An
// explicit rejection durably fails only its task and leaves supervision alive.
func (supervisor *candidateSupervisor) Run(ctx context.Context) error {
	if supervisor == nil || ctx == nil {
		return errors.New("run candidate supervisor: supervisor and context are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		tasks, err := supervisor.config.Store.ListTasks(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("run candidate supervisor: durable task queue is unavailable")
		}
		for _, task := range tasks {
			if task.State != domain.TaskValidating {
				continue
			}
			_, judgment, err := supervisor.ValidateTask(ctx, task.Handle)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if errors.Is(err, errCandidatePullRequestTruthUnavailable) {
					continue
				}
				return fmt.Errorf("run candidate supervisor: %w", err)
			}
			switch judgment.Outcome {
			case domain.CandidateAccepted:
			case domain.CandidateUnknown:
				continue
			case domain.CandidateRejected:
			default:
				return errors.New("run candidate supervisor: candidate evidence outcome is invalid")
			}
		}
		timer := time.NewTimer(supervisor.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// ValidateTask produces evidence only from current service, Git, process,
// artifact, and forge facts, then delegates the sole durable domain judgment.
func (supervisor *candidateSupervisor) ValidateTask(
	ctx context.Context,
	taskHandle string,
) (domain.Task, domain.CandidateJudgment, error) {
	if supervisor == nil || ctx == nil || domain.ValidateTaskHandle(taskHandle) != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: supervisor, context, and task are required")
	}
	if err := ctx.Err(); err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	task, err := supervisor.config.Store.GetTask(ctx, taskHandle)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, fmt.Errorf("validate task candidate: read task: %w", err)
	}
	if task.Handle != taskHandle || task.State != domain.TaskValidating {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: task is not validating")
	}
	preparation, err := supervisor.config.Store.GetManagedRunPreparation(ctx, taskHandle)
	if err != nil || preparation.RequestedWorkspaceRoot == "" {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: durable worktree is unavailable")
	}
	profile, err := supervisor.config.Catalog.ResolveProfile(task.ValidationProfile)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: reviewed profile is unavailable")
	}
	reports, err := supervisor.config.Store.ListAcceptedReports(ctx, taskHandle)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: decision inventory is unavailable")
	}
	openDecisions := unresolvedDecisionCount(reports)
	if openDecisions != 0 {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: unresolved decisions remain")
	}
	snapshot, err := supervisor.config.Git.InspectCandidate(ctx, devgit.CandidateSnapshotRequest{
		TaskHandle: taskHandle, RepositoryID: task.RepositoryID, WorktreePath: preparation.RequestedWorkspaceRoot,
	})
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: Git evidence is unavailable")
	}
	receipts, requiredLocal, err := supervisor.runLocalChecks(ctx, task, profile, snapshot)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	afterChecks, err := supervisor.config.Git.InspectCandidate(ctx, devgit.CandidateSnapshotRequest{
		TaskHandle: taskHandle, RepositoryID: task.RepositoryID, WorktreePath: preparation.RequestedWorkspaceRoot,
	})
	if err != nil || afterChecks != snapshot {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: Git evidence changed during validation")
	}
	bundle := domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: snapshot.HeadRevision,
		WorktreeCleanliness: candidateCleanliness(snapshot.Cleanliness), ValidationReceipts: receipts,
		UnresolvedDecisionCount: openDecisions,
	}
	requiredForge, material, err := supervisor.attachDeliveryEvidence(ctx, task, profile, snapshot, &bundle)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	producedAt := supervisor.config.Clock()
	if producedAt.IsZero() || producedAt.Location() != time.UTC {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: evidence time is invalid")
	}
	bundle.ProducedAt = producedAt
	bundle.ExpiresAt = producedAt.Add(profile.EvidenceTTL).UTC()
	sealed, err := domain.SealDeliveryEvidence(bundle)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: evidence could not be sealed")
	}
	publications, err := candidateEvidencePublications(task, sealed, material)
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, err
	}
	return supervisor.config.Store.CommitCandidateEvidence(
		ctx, taskHandle, sealed, requiredLocal, requiredForge, producedAt, publications,
	)
}

func (supervisor *candidateSupervisor) runLocalChecks(
	ctx context.Context,
	task domain.Task,
	profile validation.Profile,
	snapshot devgit.CandidateSnapshot,
) ([]domain.ValidationEvidenceReceipt, []string, error) {
	receipts := make([]domain.ValidationEvidenceReceipt, 0, len(profile.LocalChecks))
	required := make([]string, 0, len(profile.LocalChecks))
	fields := validation.TaskFields{
		TaskHandle: task.Handle, WorktreePath: snapshot.WorktreePath,
		BaseRevision: task.BaseRevision, HeadRevision: snapshot.HeadRevision,
	}
	for _, check := range profile.LocalChecks {
		operationID, operationErr := supervisor.config.NewValidationOperationID()
		if operationErr != nil {
			return nil, nil, errors.New("validate task candidate: validation process identity is unavailable")
		}
		receipt, runErr := supervisor.config.Runner.Run(ctx, validation.RunRequest{
			OperationID: operationID, TaskHandle: task.Handle, ProfileID: profile.ID, CheckID: check.ID, Fields: fields,
		})
		if !completeValidationReceipt(receipt, operationID, task, profile, check, snapshot) {
			return nil, nil, errors.New("validate task candidate: validation receipt is incomplete")
		}
		conclusion := domain.CheckFailed
		if runErr == nil && receipt.Passed {
			conclusion = domain.CheckPassed
		}
		receipts = append(receipts, domain.ValidationEvidenceReceipt{
			CheckID: check.ID, ProgramID: receipt.ProgramID, HeadRevision: receipt.HeadRevision,
			Conclusion: conclusion, Required: check.Required, OutputHash: receipt.OutputHash,
			StartedAt: receipt.StartedAt, CompletedAt: receipt.CompletedAt,
		})
		if check.Required {
			required = append(required, check.ID)
		}
	}
	if len(required) == 0 {
		return nil, nil, errors.New("validate task candidate: profile has no required local check")
	}
	return receipts, required, nil
}

func (supervisor *candidateSupervisor) attachDeliveryEvidence(
	ctx context.Context,
	task domain.Task,
	profile validation.Profile,
	snapshot devgit.CandidateSnapshot,
	bundle *domain.DeliveryEvidenceBundle,
) ([]string, candidateDeliveryMaterial, error) {
	switch task.Shape {
	case domain.ShapeShip:
		required := requiredForgeCheckNames(profile.ForgeChecks)
		if len(required) == 0 {
			return nil, candidateDeliveryMaterial{}, errors.New("validate task candidate: ship profile has no required forge check")
		}
		truth, err := supervisor.config.PullRequests.DeliverPullRequest(ctx, forge.PullRequestRequest{
			OperationID:  candidateDeliveryOperationID(task.Handle, snapshot.HeadRevision),
			WorktreePath: snapshot.WorktreePath, Branch: snapshot.Branch, HeadRevision: snapshot.HeadRevision,
			Title: "Task " + task.Handle, RequiredChecks: required,
		})
		if err != nil {
			return nil, candidateDeliveryMaterial{}, errCandidatePullRequestTruthUnavailable
		}
		bundle.ForgeEvidence = &truth.Evidence
		return required, candidateDeliveryMaterial{referenceURL: truth.URL}, nil
	case domain.ShapeScout:
		if len(profile.ArtifactRules) != 1 {
			return nil, candidateDeliveryMaterial{}, errors.New("validate task candidate: scout artifact rule is unavailable")
		}
		rule := profile.ArtifactRules[0]
		artifact, err := supervisor.config.InspectArtifact(
			ctx, filepath.Join(snapshot.WorktreePath, rule.RelativePath), rule.MaxBytes, rule.MediaType,
		)
		if err != nil {
			return nil, candidateDeliveryMaterial{}, errors.New("validate task candidate: report artifact is unavailable")
		}
		bundle.ReportArtifact = &artifact.ReportArtifactEvidence
		return nil, candidateDeliveryMaterial{
			artifact: &artifact, fileName: filepath.Base(rule.RelativePath),
		}, nil
	default:
		return nil, candidateDeliveryMaterial{}, errors.New("validate task candidate: task shape is invalid")
	}
}

func candidateEvidencePublications(
	task domain.Task,
	sealed *domain.SealedDeliveryEvidence,
	material candidateDeliveryMaterial,
) ([]application.ComisEvidencePublication, error) {
	if sealed == nil || sealed.Digest() == "" || task.ManagedRunID == "" {
		return nil, errors.New("validate task candidate: publication identity is unavailable")
	}
	bundle := sealed.Bundle()
	expiresAt := bundle.ExpiresAt
	publications := []application.ComisEvidencePublication{
		candidateEvidencePublication(
			task.Handle, sealed.Digest(), "bundle", "candidate_bundle", sealed.Canonical(),
			bundle.ProducedAt, &expiresAt, nil,
		),
	}
	switch {
	case bundle.ForgeEvidence != nil && material.referenceURL != "":
		publications = append(publications, candidateEvidencePublication(
			task.Handle, sealed.Digest(), "delivery", "delivery_reference", []byte(material.referenceURL),
			bundle.ProducedAt, &expiresAt,
			&application.ComisEvidenceDeliveryRequest{Kind: application.ComisEvidenceReference},
		))
	case bundle.ReportArtifact != nil && material.artifact != nil:
		body := material.artifact.Body
		if int64(len(body)) != bundle.ReportArtifact.Size ||
			fmt.Sprintf("%x", sha256.Sum256(body)) != bundle.ReportArtifact.ContentHash ||
			material.artifact.MediaType != bundle.ReportArtifact.MediaType || material.fileName == "" {
			return nil, errors.New("validate task candidate: report publication differs from inspected artifact")
		}
		publications = append(publications, candidateEvidencePublication(
			task.Handle, sealed.Digest(), "delivery", "report_artifact", body,
			bundle.ProducedAt, &expiresAt,
			&application.ComisEvidenceDeliveryRequest{
				Kind: application.ComisEvidenceAttachment, FileName: material.fileName,
				MediaType: bundle.ReportArtifact.MediaType,
			},
		))
	default:
		return nil, errors.New("validate task candidate: delivery publication is unavailable")
	}
	return publications, nil
}

func candidateEvidencePublication(
	taskHandle string,
	subjectDigest string,
	suffix string,
	kind string,
	body []byte,
	observedAt time.Time,
	expiresAt *time.Time,
	deliveryRequest *application.ComisEvidenceDeliveryRequest,
) application.ComisEvidencePublication {
	evidenceRef := "evidence-" + suffix + "-" + subjectDigest[:32]
	operationDigest := sha256.Sum256([]byte(taskHandle + "\x00" + evidenceRef))
	return application.ComisEvidencePublication{
		OperationID: "put-evidence-" + fmt.Sprintf("%x", operationDigest[:16]),
		TaskHandle:  taskHandle, EvidenceRef: evidenceRef, Kind: kind,
		SubjectDigest: subjectDigest, ObservedAt: observedAt, ExpiresAt: expiresAt,
		ContentHash:       fmt.Sprintf("%x", sha256.Sum256(body)),
		VerificationLevel: application.ComisEvidenceAdapterVerified,
		Body:              append([]byte(nil), body...), Delivery: deliveryRequest,
	}
}

func unresolvedDecisionCount(reports []domain.AcceptedReport) int {
	open := make(map[string]struct{})
	for _, accepted := range reports {
		switch accepted.Report.Kind {
		case domain.ReportDecision:
			open[accepted.Report.ExternalKey] = struct{}{}
		case domain.ReportResolution:
			delete(open, accepted.Report.ExternalKey)
		}
	}
	return len(open)
}

func requiredForgeCheckNames(checks []validation.ForgeCheck) []string {
	required := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Required {
			required = append(required, check.Name)
		}
	}
	return required
}

func completeValidationReceipt(
	receipt validation.Receipt,
	operationID string,
	task domain.Task,
	profile validation.Profile,
	check validation.LocalCheck,
	snapshot devgit.CandidateSnapshot,
) bool {
	return receipt.OperationID == operationID &&
		receipt.TaskHandle == task.Handle && receipt.ProfileID == profile.ID && receipt.CheckID == check.ID &&
		receipt.ProgramID == check.ProgramID && receipt.HeadRevision == snapshot.HeadRevision &&
		receipt.StartedAt.Location() == time.UTC && receipt.CompletedAt.Location() == time.UTC &&
		!receipt.CompletedAt.Before(receipt.StartedAt) && len(receipt.OutputHash) == 64
}

func candidateCleanliness(cleanliness devgit.CandidateCleanliness) domain.WorktreeCleanliness {
	if cleanliness == devgit.CandidateClean {
		return domain.WorktreeClean
	}
	if cleanliness == devgit.CandidateDirty {
		return domain.WorktreeDirty
	}
	return domain.WorktreeUnknown
}

func candidateDeliveryOperationID(taskHandle, head string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(taskHandle+"\x00"+head)))
	return "delivery-" + digest[:32]
}

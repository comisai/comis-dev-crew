package livecampaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func (runner CampaignRunner) waitOperationAfterCheckpoint(
	ctx context.Context,
	manifest Manifest,
	command string,
	minimumEpochMs int64,
) (string, error) {
	operation, found := operationForCommand(manifest, command)
	if !found {
		return "", fmt.Errorf("run protected live campaign: %s operation is absent from the manifest", command)
	}
	stage := strings.TrimSuffix(strings.ToLower(command), "task") + " operation after Telegram checkpoint"
	resolvedOperationID := operation.OperationID
	err := runner.wait(ctx, stage, func() (bool, error) {
		if resolvedOperationID == "" {
			var err error
			resolvedOperationID, err = runner.resolveOperationIdentity(ctx, manifest, operation.TaskHandle, command)
			if err != nil {
				return false, nil
			}
		}
		view, err := runner.readOperation(ctx, manifest, resolvedOperationID)
		if err != nil {
			return false, nil
		}
		resolved := operation
		resolved.OperationID = resolvedOperationID
		if err := VerifyOperation(resolved, view); err != nil {
			return false, nil
		}
		return view.UpdatedAtMs >= minimumEpochMs, nil
	})
	return resolvedOperationID, err
}

func (runner CampaignRunner) resolveOperationIdentity(
	ctx context.Context,
	manifest Manifest,
	taskHandle string,
	command string,
) (string, error) {
	detail, err := runner.readTaskDetail(ctx, manifest, taskHandle)
	if err != nil {
		return "", err
	}
	operationID := ""
	switch command {
	case "ReconcileTask":
		operationID = detail.Evidence.Candidate.ReconciliationOperationID
	case "HandbackTask":
		if detail.Evidence.Activity.ReportKind == domain.ReportCandidateComplete {
			operationID = detail.Evidence.Activity.ReportID
		}
	case "CleanupTask":
		operationID = detail.Evidence.Cleanup.OperationID
	default:
		return "", errors.New("resolve DevCrew operation identity: command is outside the live catalog")
	}
	if err := domain.ValidateOperationID(operationID); err != nil {
		return "", errors.New("resolve DevCrew operation identity: task evidence is not ready")
	}
	return operationID, nil
}

func (runner CampaignRunner) readTaskDetail(
	ctx context.Context,
	manifest Manifest,
	taskHandle string,
) (application.TaskDetail, error) {
	output, err := runner.Executor.Run(ctx, Command{
		Path: manifest.DevCrew.CLIPath,
		Args: []string{"--socket", manifest.DevCrew.SocketPath, "task", "show", taskHandle, "--format", "json"},
	})
	if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes {
		return application.TaskDetail{}, errors.New("read DevCrew task detail: report unavailable")
	}
	var detail application.TaskDetail
	if err := json.Unmarshal(output, &detail); err != nil {
		return application.TaskDetail{}, errors.New("read DevCrew task detail: report is malformed")
	}
	if detail.SchemaVersion != 1 || detail.Completeness != application.CompletenessComplete ||
		detail.Summary.TaskHandle != taskHandle {
		return application.TaskDetail{}, errors.New("read DevCrew task detail: report is incomplete or mismatched")
	}
	return detail, nil
}

func bindOperationIdentity(manifest *Manifest, command string, operationID string) error {
	for _, operation := range manifest.Operations {
		if operation.Command == command {
			return bindTaskOperationIdentity(manifest, operation.TaskHandle, command, operationID)
		}
	}
	return fmt.Errorf("bind DevCrew operation identity: %s operation is absent", command)
}

func bindTaskOperationIdentity(manifest *Manifest, taskHandle string, command string, operationID string) error {
	if err := domain.ValidateOperationID(operationID); err != nil {
		return errors.New("bind DevCrew operation identity: resolved identity is invalid")
	}
	for index := range manifest.Operations {
		operation := &manifest.Operations[index]
		if operation.TaskHandle != taskHandle || operation.Command != command {
			if operation.OperationID == operationID {
				return errors.New("bind DevCrew operation identity: resolved identity is not distinct")
			}
			continue
		}
		if operation.OperationID != "" && operation.OperationID != operationID {
			return errors.New("bind DevCrew operation identity: durable identity differs from the manifest")
		}
		operation.OperationID = operationID
		return nil
	}
	return errors.New("bind DevCrew operation identity: task command is absent from the manifest")
}

func (runner CampaignRunner) readOperation(
	ctx context.Context,
	manifest Manifest,
	operationID string,
) (application.OperationView, error) {
	output, err := runner.Executor.Run(ctx, Command{
		Path: manifest.DevCrew.CLIPath,
		Args: []string{
			"--socket", manifest.DevCrew.SocketPath, "task", "operation", operationID, "--format", "json",
		},
	})
	if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes {
		return application.OperationView{}, errors.New("read DevCrew operation: report unavailable")
	}
	var view application.OperationView
	if err := json.Unmarshal(output, &view); err != nil {
		return application.OperationView{}, errors.New("read DevCrew operation: report is malformed")
	}
	return view, nil
}

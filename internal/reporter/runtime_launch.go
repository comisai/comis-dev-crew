package reporter

import (
	"errors"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func validateRuntimeLaunchConfig(config RuntimeServerConfig) error {
	if (config.AttentionResponses == nil) != (config.NewAttentionOperationID == nil) {
		return errors.New("listen runtime attachment: attention response dependencies are incomplete")
	}
	empty := application.LaunchAcknowledgement{}
	configured := config.LaunchOperationID != "" || config.ExpectedLaunch != empty || config.LaunchAcknowledger != nil
	if !configured {
		return nil
	}
	return validateRuntimeLaunchBinding(config.Brief, config.Reporter, RuntimeLaunchConfig{
		OperationID: config.LaunchOperationID, Expected: config.ExpectedLaunch, Acknowledger: config.LaunchAcknowledger,
	})
}

func validateRuntimeLaunchBinding(brief domain.WorkerBrief, reporter *Client, config RuntimeLaunchConfig) error {
	canonicalWorkspace, workspaceErr := filepath.EvalSymlinks(config.Expected.WorkingDirectory)
	if domain.ValidateOperationID(config.OperationID) != nil || config.Expected.Validate() != nil || config.Acknowledger == nil ||
		workspaceErr != nil || canonicalWorkspace != config.Expected.WorkingDirectory ||
		config.Expected.BriefRevision != brief.Revision || config.Expected.BriefRevisionHash != brief.RevisionHash {
		return errors.New("listen runtime attachment: launch acknowledgement binding is invalid")
	}
	if reporter == nil || reporter.endpoint == nil || reporter.endpoint.taskHandle != config.Expected.TaskHandle ||
		reporter.endpoint.briefRevision != config.Expected.BriefRevision ||
		reporter.endpoint.briefRevisionHash != config.Expected.BriefRevisionHash {
		return errors.New("listen runtime attachment: launch and report scopes differ")
	}
	return nil
}

func (server *RuntimeServer) launchBinding() *RuntimeLaunchConfig {
	server.launchMu.RLock()
	defer server.launchMu.RUnlock()
	if server.launch == nil {
		return nil
	}
	binding := *server.launch
	return &binding
}

func runtimeRejected(code string) RuntimeOutcome {
	return RuntimeOutcome{Version: runtimeProtocolVersion, Error: &RuntimeError{Code: code}}
}

func validLaunchAcknowledgementResult(
	result application.MutationResult,
	operationID string,
	expected application.LaunchAcknowledgement,
) bool {
	return result.Operation.ID == operationID && result.Operation.Status == domain.OperationCompleted &&
		(result.Task.State == domain.TaskLaunching || result.Task.State == domain.TaskWorking) &&
		result.Task.Handle == expected.TaskHandle && result.Task.ManagedRunID == expected.ManagedRunID &&
		result.Task.WorkspaceLeaseID == expected.WorkspaceLeaseID && result.Task.BriefRevision == expected.BriefRevision &&
		result.Task.BriefRevisionHash == expected.BriefRevisionHash
}

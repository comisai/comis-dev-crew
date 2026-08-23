package reporter

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// BindLaunch attaches one exact activation identity without replacing the
// socket Comis already validated. Altered replays fail closed.
func (server *RuntimeServer) BindLaunch(config RuntimeLaunchConfig) error {
	if server == nil || server.reporter == nil {
		return errors.New("bind runtime launch: server is unavailable")
	}
	if err := validateRuntimeLaunchBinding(server.brief, server.reporter, config); err != nil {
		return err
	}
	server.launchMu.Lock()
	defer server.launchMu.Unlock()
	if server.launch != nil {
		if server.launch.OperationID != config.OperationID || server.launch.Expected != config.Expected {
			return errors.New("bind runtime launch: activation binding conflicts")
		}
		return nil
	}
	binding := config
	server.launch = &binding
	return nil
}

// RebindLaunch rotates only the operation identity for a later launch of the
// same task generation authority. The brief, workspace, run, lease, and
// acknowledger must remain exact; replacement uses a separate attachment
// generation because its brief changes.
func (server *RuntimeServer) RebindLaunch(config RuntimeLaunchConfig) error {
	if server == nil {
		return errors.New("rebind runtime launch: server is unavailable")
	}
	server.launchMu.Lock()
	defer server.launchMu.Unlock()
	if server.reporter == nil {
		return errors.New("rebind runtime launch: server is unavailable")
	}
	if err := validateRuntimeLaunchBinding(server.brief, server.reporter, config); err != nil {
		return err
	}
	if server.launch == nil || server.launch.Expected != config.Expected {
		return errors.New("rebind runtime launch: authority conflicts")
	}
	if server.launch.OperationID == config.OperationID {
		return nil
	}
	binding := config
	server.launch = &binding
	return nil
}

// RebindGeneration rotates the immutable brief, reporter scope, and launch
// operation together for a replacement worker while retaining the exact task,
// run, lease, workspace, socket, and private reporter credential.
func (server *RuntimeServer) RebindGeneration(brief domain.WorkerBrief, config RuntimeLaunchConfig) error {
	if server == nil {
		return errors.New("rebind runtime generation: server is unavailable")
	}
	server.launchMu.Lock()
	defer server.launchMu.Unlock()
	if server.reporter == nil || server.reporter.endpoint == nil || server.launch == nil || brief.Validate() != nil ||
		!sameRuntimeLaunchAuthority(server.launch.Expected, config.Expected) {
		return errors.New("rebind runtime generation: authority conflicts")
	}
	prior := server.reporter.endpoint
	endpoint, err := NewEndpoint(EndpointConfig{
		TaskHandle: config.Expected.TaskHandle, BriefRevision: brief.Revision,
		BriefRevisionHash: brief.RevisionHash, Credential: server.reporter.credential,
		Sink: prior.sink, Auditor: prior.auditor, Logger: prior.logger, Clock: prior.clock,
	})
	if err != nil {
		return errors.New("rebind runtime generation: reporter scope is unavailable")
	}
	client, err := NewClient(endpoint, server.reporter.credential)
	if err != nil || validateRuntimeLaunchBinding(brief, client, config) != nil {
		return errors.New("rebind runtime generation: launch scope is unavailable")
	}
	server.brief = brief
	server.reporter = client
	binding := config
	server.launch = &binding
	return nil
}

func sameRuntimeLaunchAuthority(left, right application.LaunchAcknowledgement) bool {
	return left.TaskHandle == right.TaskHandle && left.ManagedRunID == right.ManagedRunID &&
		left.WorkspaceLeaseID == right.WorkspaceLeaseID && left.WorkingDirectory == right.WorkingDirectory
}

func (server *RuntimeServer) generationBinding() (domain.WorkerBrief, *Client) {
	server.launchMu.RLock()
	defer server.launchMu.RUnlock()
	return server.brief, server.reporter
}

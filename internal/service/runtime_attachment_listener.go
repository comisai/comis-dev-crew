package service

import (
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func (coordinator *runtimeAttachmentCoordinator) listenRuntimeAttachment(
	request application.RuntimeAttachmentPreparationRequest,
	attachment application.PreparedRuntimeAttachment,
	priorTaskIdentity *reporter.RuntimeSocketIdentity,
) (*runtimeAttachmentEntry, error) {
	credential, err := coordinator.newCredential()
	if err != nil {
		return nil, errors.New("prepare runtime attachment: credential source failed")
	}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: request.TaskHandle, BriefRevision: request.BriefRevision,
		BriefRevisionHash: request.BriefRevisionHash, Credential: credential, Sink: coordinator.reportSink,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare runtime attachment endpoint: %w", err)
	}
	client, err := reporter.NewClient(endpoint, credential)
	if err != nil {
		return nil, fmt.Errorf("prepare runtime attachment client: %w", err)
	}
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: attachment.SourcePath, Brief: request.Brief, Reporter: client,
		AttentionResponses: coordinator, NewAttentionOperationID: coordinator.newAttentionOperationID,
	})
	if err != nil {
		return nil, err
	}
	identity, err := server.SocketIdentity()
	if err != nil {
		return nil, errors.Join(err, server.Close())
	}
	if err := coordinator.persistRuntimeAttachmentIdentityWithPrior(
		request.TaskHandle, identity, priorTaskIdentity,
	); err != nil {
		return nil, errors.Join(err, server.Close())
	}
	return &runtimeAttachmentEntry{request: request, attachment: attachment, server: server}, nil
}

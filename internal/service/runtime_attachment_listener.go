package service

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

func (coordinator *runtimeAttachmentCoordinator) listenRuntimeAttachment(
	request application.RuntimeAttachmentPreparationRequest,
	attachment application.PreparedRuntimeAttachment,
) (*runtimeAttachmentEntry, error) {
	pinned, temporaryName, priorRecord, err := coordinator.prepareRuntimeAttachmentDirectory(request.TaskHandle)
	if err != nil {
		return nil, err
	}
	temporaryAttachment := attachment
	temporaryAttachment.SourcePath = filepath.Join(coordinator.runtimeRoot, temporaryName, "attachment.sock")
	credential, err := coordinator.newCredential()
	if err != nil {
		return nil, errors.Join(errors.New("prepare runtime attachment: credential source failed"), pinned.close())
	}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: request.TaskHandle, BriefRevision: request.BriefRevision,
		BriefRevisionHash: request.BriefRevisionHash, Credential: credential, Sink: coordinator.reportSink,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("prepare runtime attachment endpoint: %w", err), pinned.close())
	}
	client, err := reporter.NewClient(endpoint, credential)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("prepare runtime attachment client: %w", err), pinned.close())
	}
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: temporaryAttachment.SourcePath, Brief: request.Brief, Reporter: client,
		AttentionResponses: coordinator, NewAttentionOperationID: coordinator.newAttentionOperationID,
	})
	if err != nil {
		return nil, errors.Join(err, pinned.close())
	}
	identity, err := server.SocketIdentity()
	if err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	if err := unix.Fsync(pinned.taskDescriptor); err != nil {
		return nil, errors.Join(errors.New("prepare runtime attachment: prepared directory cannot be synchronized"), server.Close(), pinned.close())
	}
	preparedIdentity, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || !sameRuntimeAttachmentStableNode(preparedIdentity, pinned.taskIdentity) {
		return nil, errors.Join(errors.New("prepare runtime attachment: prepared directory identity differs"), server.Close(), pinned.close())
	}
	pinned.taskIdentity = preparedIdentity
	creating := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: preparedIdentity, Socket: identity,
	}
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, creating, priorRecord, nil); err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	if err := reporter.PublishRuntimeDirectory(
		pinned.runtimeRootDescriptor, temporaryName, request.TaskHandle, pinned.taskIdentity, 0o700,
	); err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	if coordinator.afterRuntimeDirectoryPublish != nil {
		if err := coordinator.afterRuntimeDirectoryPublish(); err != nil {
			return nil, errors.Join(err, server.Close(), pinned.close())
		}
	}
	if err := server.RelocateSocket(attachment.SourcePath); err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || !sameRuntimeAttachmentStableNode(current, creating.Task) {
		return nil, errors.Join(errors.New("prepare runtime attachment: published directory identity differs"), server.Close(), pinned.close())
	}
	active := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentActive, Task: current, Socket: identity}
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, active, &creating, nil); err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	if err := pinned.close(); err != nil {
		return nil, errors.Join(err, server.Close())
	}
	return &runtimeAttachmentEntry{request: request, attachment: attachment, server: server}, nil
}

func (coordinator *runtimeAttachmentCoordinator) prepareRuntimeAttachmentDirectory(
	taskHandle string,
) (*pinnedTaskRuntimeDirectory, string, *runtimeAttachmentIdentityRecord, error) {
	runtimeRootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		return nil, "", nil, err
	}
	prior, _, priorFound, err := readRuntimeAttachmentIdentityRecord(runtimeRootDescriptor, taskHandle)
	if err != nil {
		return nil, "", nil, errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	var priorRecord *runtimeAttachmentIdentityRecord
	if priorFound {
		priorRecord = &prior
	}
	if !runtimeAttachmentPathAbsent(runtimeRootDescriptor, taskHandle) {
		return nil, "", nil, errors.Join(
			errors.New("prepare runtime attachment: task runtime directory already exists"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	temporaryName := runtimeAttachmentCreationName(taskHandle)
	temporaryExists := !runtimeAttachmentPathAbsent(runtimeRootDescriptor, temporaryName)
	if temporaryExists && (!priorFound || prior.Stage != runtimeAttachmentCreatingIntent) {
		return nil, "", nil, errors.Join(
			errors.New("prepare runtime attachment: staged directory identity is unproven"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	intent := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentCreatingIntent}
	if _, err := publishRuntimeAttachmentIdentity(
		runtimeRootDescriptor, taskHandle, intent, priorRecord, nil,
	); err != nil {
		return nil, "", nil, errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	priorRecord = &intent
	if !temporaryExists {
		if err := unix.Mkdirat(runtimeRootDescriptor, temporaryName, 0o700); err != nil {
			return nil, "", nil, errors.Join(
				errors.New("prepare runtime attachment: staged directory is unavailable"),
				closeRuntimeRootDescriptor(runtimeRootDescriptor),
			)
		}
	}
	if err := unix.Fsync(runtimeRootDescriptor); err != nil {
		return nil, "", nil, errors.Join(
			errors.New("prepare runtime attachment: staged directory cannot be synchronized"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(runtimeRootDescriptor, temporaryName)
	if err != nil || missing {
		return nil, "", nil, errors.Join(
			errors.New("prepare runtime attachment: staged directory identity is unavailable"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, directoryName: temporaryName, taskIdentity: taskIdentity,
	}
	if coordinator.afterRuntimeDirectoryCreation != nil {
		if err := coordinator.afterRuntimeDirectoryCreation(); err != nil {
			return nil, "", nil, errors.Join(err, pinned.close())
		}
	}
	return pinned, temporaryName, priorRecord, nil
}

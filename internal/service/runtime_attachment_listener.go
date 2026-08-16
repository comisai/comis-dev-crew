package service

import (
	"crypto/sha256"
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
	credential, err := coordinator.newCredential()
	if err != nil {
		return nil, errors.New("prepare runtime attachment: credential source failed")
	}
	proposedRelaySeed := sha256.Sum256([]byte("runtime-relay\x00" + credential))
	pinned, temporaryName, priorRecord, relaySeed, err := coordinator.prepareRuntimeAttachmentDirectory(
		request.TaskHandle, proposedRelaySeed,
	)
	if err != nil {
		return nil, err
	}
	temporaryAttachment := attachment
	temporaryAttachment.SourcePath = filepath.Join(coordinator.runtimeRoot, temporaryName, "attachment.sock")
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
		RelaySeed: relaySeed[:],
	})
	if err != nil {
		return nil, errors.Join(err, pinned.close())
	}
	if coordinator.afterRuntimeSocketListen != nil {
		if err := coordinator.afterRuntimeSocketListen(server); err != nil {
			return nil, errors.Join(err, pinned.close())
		}
	}
	if attachment.RelayIdentity != "" && attachment.RelayIdentity != server.RelayIdentity() {
		return nil, errors.Join(errors.New("prepare runtime attachment: relay identity differs"), server.Close(), pinned.close())
	}
	attachment.RelayIdentity = server.RelayIdentity()
	identity, err := server.SocketIdentity()
	if err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	if err := unix.Fsync(pinned.taskDescriptor); err != nil {
		return nil, errors.Join(errors.New("prepare runtime attachment: prepared directory cannot be synchronized"), server.Close(), pinned.close())
	}
	preparedIdentity, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || !sameRuntimeAttachmentNode(preparedIdentity, pinned.taskIdentity) ||
		!runtimeAttachmentGenerationMatches(pinned, priorRecord.Generation, priorRecord.GenerationID) {
		return nil, errors.Join(errors.New("prepare runtime attachment: prepared directory identity differs"), server.Close(), pinned.close())
	}
	pinned.taskIdentity = preparedIdentity
	creating := *priorRecord
	creating.Task = preparedIdentity
	creating.Socket = identity
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, creating, priorRecord, nil); err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	publishedIdentity, err := reporter.PublishRuntimeDirectoryIdentity(
		pinned.runtimeRootDescriptor, temporaryName, request.TaskHandle, pinned.taskIdentity, 0o700,
	)
	if err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	pinned.taskIdentity = publishedIdentity
	pinned.directoryName = request.TaskHandle
	if coordinator.afterRuntimeDirectoryPublish != nil {
		if err := coordinator.afterRuntimeDirectoryPublish(); err != nil {
			return nil, errors.Join(err, server.Close(), pinned.close())
		}
	}
	if err := server.RelocateSocket(attachment.SourcePath); err != nil {
		return nil, errors.Join(err, server.Close(), pinned.close())
	}
	current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || !sameRuntimeAttachmentNode(current, creating.Task) ||
		!runtimeAttachmentGenerationMatches(pinned, creating.Generation, creating.GenerationID) {
		return nil, errors.Join(errors.New("prepare runtime attachment: published directory identity differs"), server.Close(), pinned.close())
	}
	active := creating
	active.Stage = runtimeAttachmentActive
	active.Task = current
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
	proposedRelaySeed [32]byte,
) (*pinnedTaskRuntimeDirectory, string, *runtimeAttachmentIdentityRecord, [32]byte, error) {
	runtimeRootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		return nil, "", nil, [32]byte{}, err
	}
	prior, _, priorFound, err := readRuntimeAttachmentIdentityRecord(runtimeRootDescriptor, taskHandle)
	if err != nil {
		return nil, "", nil, [32]byte{}, errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	var priorRecord *runtimeAttachmentIdentityRecord
	if priorFound {
		priorRecord = &prior
		proposedRelaySeed = prior.RelaySeed
	}
	if !runtimeAttachmentPathAbsent(runtimeRootDescriptor, taskHandle) {
		return nil, "", nil, [32]byte{}, errors.Join(
			errors.New("prepare runtime attachment: task runtime directory already exists"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	temporaryName := runtimeAttachmentCreationName(taskHandle)
	temporaryExists := !runtimeAttachmentPathAbsent(runtimeRootDescriptor, temporaryName)
	if temporaryExists && (!priorFound || prior.Stage == runtimeAttachmentCreatingIntent ||
		prior.Stage != runtimeAttachmentDirectoryBound &&
			(prior.Stage != runtimeAttachmentCreating || prior.Socket.Valid())) {
		return nil, "", nil, [32]byte{}, errors.Join(
			errors.New("prepare runtime attachment: staged directory identity is unproven"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	var generation reporter.RuntimeSocketIdentity
	var generationID [16]byte
	if priorFound {
		generation, generationID = prior.Generation, prior.GenerationID
		if !runtimeAttachmentGenerationAvailable(runtimeRootDescriptor, generation, generationID) {
			return nil, "", nil, [32]byte{}, errors.Join(
				errors.New("prepare runtime attachment: generation authority differs"),
				closeRuntimeRootDescriptor(runtimeRootDescriptor),
			)
		}
	} else {
		generation, generationID, err = createRuntimeAttachmentGeneration(runtimeRootDescriptor, taskHandle)
		if err != nil {
			return nil, "", nil, [32]byte{}, errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
		}
	}
	intent := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation,
		GenerationID: generationID, RelaySeed: proposedRelaySeed,
	}
	if !temporaryExists {
		if !priorFound || prior != intent {
			if _, err := publishRuntimeAttachmentIdentity(
				runtimeRootDescriptor, taskHandle, intent, priorRecord, nil,
			); err != nil {
				return nil, "", nil, [32]byte{}, errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
			}
		}
		priorRecord = &intent
		if err := unix.Mkdirat(runtimeRootDescriptor, temporaryName, 0o700); err != nil {
			return nil, "", nil, [32]byte{}, errors.Join(
				errors.New("prepare runtime attachment: staged directory is unavailable"),
				closeRuntimeRootDescriptor(runtimeRootDescriptor),
			)
		}
	}
	if err := unix.Fsync(runtimeRootDescriptor); err != nil {
		return nil, "", nil, [32]byte{}, errors.Join(
			errors.New("prepare runtime attachment: staged directory cannot be synchronized"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(runtimeRootDescriptor, temporaryName)
	if err != nil || missing {
		return nil, "", nil, [32]byte{}, errors.Join(
			errors.New("prepare runtime attachment: staged directory identity is unavailable"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, directoryName: temporaryName, taskIdentity: taskIdentity,
	}
	if priorRecord.Stage == runtimeAttachmentCreatingIntent {
		directoryBound := intent
		directoryBound.Stage = runtimeAttachmentDirectoryBound
		directoryBound.Task = taskIdentity
		if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, directoryBound, priorRecord, nil); err != nil {
			return nil, "", nil, [32]byte{}, errors.Join(err, pinned.close())
		}
		priorRecord = &directoryBound
	} else if !runtimeAttachmentTransitionDirectoryMatches(
		taskIdentity,
		priorRecord.Task,
		runtimeAttachmentGenerationMatches(pinned, priorRecord.Generation, priorRecord.GenerationID),
	) {
		return nil, "", nil, [32]byte{}, errors.Join(
			errors.New("prepare runtime attachment: staged directory identity differs"), pinned.close(),
		)
	}
	if priorRecord.Stage == runtimeAttachmentDirectoryBound {
		generation, err = linkRuntimeAttachmentGeneration(pinned, generation, generationID)
		if err != nil {
			return nil, "", nil, [32]byte{}, errors.Join(err, pinned.close())
		}
		boundIdentity, identityErr := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
		if identityErr != nil {
			return nil, "", nil, [32]byte{}, errors.Join(identityErr, pinned.close())
		}
		pinned.taskIdentity = boundIdentity
		creating := *priorRecord
		creating.Stage = runtimeAttachmentCreating
		creating.Task = boundIdentity
		creating.Generation = generation
		if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, creating, priorRecord, nil); err != nil {
			return nil, "", nil, [32]byte{}, errors.Join(err, pinned.close())
		}
		priorRecord = &creating
	} else if !runtimeAttachmentGenerationMatches(pinned, priorRecord.Generation, priorRecord.GenerationID) {
		return nil, "", nil, [32]byte{}, errors.Join(
			errors.New("prepare runtime attachment: staged generation differs"), pinned.close(),
		)
	}
	if coordinator.afterRuntimeDirectoryCreation != nil {
		if err := coordinator.afterRuntimeDirectoryCreation(); err != nil {
			return nil, "", nil, [32]byte{}, errors.Join(err, pinned.close())
		}
	}
	return pinned, temporaryName, priorRecord, proposedRelaySeed, nil
}

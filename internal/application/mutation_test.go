package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestMutations_PrepareBuildsOnePinnedServiceMintedTask(t *testing.T) {
	clock := time.Date(2026, time.August, 9, 12, 30, 0, 0, time.UTC)
	store := &mutationStore{}
	repositories := &repositoryCatalog{}
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: repositories, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-0001", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_0001", nil },
		PreparationTTL:     15 * time.Minute,
		Clock:              func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	command := validPrepareCommand()
	_, err = mutations.PrepareTask(context.Background(), command)
	if err != nil {
		t.Fatalf("PrepareTask() error = %v", err)
	}
	mutation := store.prepared
	if mutation.Task.Handle != "task-0001" || mutation.Task.State != domain.TaskPrepared || mutation.Task.ServiceInstanceID != command.ServiceInstanceID {
		t.Fatalf("prepared task = %#v, want service-minted prepared record", mutation.Task)
	}
	if err := mutation.Task.Validate(); err != nil {
		t.Fatalf("prepared task validation error = %v", err)
	}
	if _, err := mutation.Task.RenderWorkerBrief(); err != nil {
		t.Fatalf("prepared task brief error = %v", err)
	}
	if mutation.OperationID != command.OperationID || len(mutation.SubjectDigest) != 64 || mutation.At != clock {
		t.Fatalf("prepared operation = %#v, want stable ID, SHA-256 subject, and injected time", mutation)
	}
	if mutation.Preparation.ExternalRunRef != mutation.Task.Handle ||
		mutation.Preparation.RegistrationNonce != "registration-nonce_0001" ||
		!mutation.Preparation.ExpiresAt.Equal(clock.Add(15*time.Minute)) {
		t.Fatalf("managed-run preparation = %#v, want durable exact join", mutation.Preparation)
	}
	if repositories.calls != 1 || repositories.repositoryID != command.RepositoryID {
		t.Fatalf("repository validation calls/id = %d/%q", repositories.calls, repositories.repositoryID)
	}
}

func TestMutations_ActivateManagedRunBuildsExactPrivateReplaySubject(t *testing.T) {
	clock := time.Date(2026, time.August, 9, 12, 31, 0, 0, time.UTC)
	store := &mutationStore{}
	attachments := testRuntimeAttachments()
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: attachments,
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-0001", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
		Clock: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	command := ActivateManagedRunCommand{
		OperationID: "op-bind-0001", ServiceInstanceID: "service-instance-0001",
		ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_0001",
		ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001",
		ExecutionAttachmentID: "execution-attachment-0001",
		AttachmentTargetName:  "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
	}
	if _, err := mutations.ActivateManagedRun(context.Background(), command); err != nil {
		t.Fatalf("ActivateManagedRun() error = %v", err)
	}
	if store.activation.ExternalRunRef != command.ExternalRunRef ||
		store.activation.RegistrationNonce != command.RegistrationNonce ||
		store.activation.Binding.ManagedRunID != command.ManagedRunID ||
		store.activation.Binding.WorkspaceLeaseID != command.WorkspaceLeaseID ||
		store.activation.ExecutionAttachmentID != command.ExecutionAttachmentID ||
		store.activation.AttachmentTargetName != command.AttachmentTargetName ||
		len(store.activation.SubjectDigest) != 64 || store.activation.At != clock {
		t.Fatalf("activation mutation = %#v, want exact stable private subject", store.activation)
	}
	if attachments.bindCalls != 1 || attachments.bindRequest.TaskHandle != command.ExternalRunRef ||
		attachments.bindRequest.ManagedRunID != command.ManagedRunID ||
		attachments.bindRequest.WorkspaceLeaseID != command.WorkspaceLeaseID ||
		attachments.bindRequest.ExecutionAttachmentID != command.ExecutionAttachmentID ||
		attachments.bindRequest.AttachmentTargetName != command.AttachmentTargetName ||
		attachments.bindRequest.LaunchOperationID == "" || attachments.bindRequest.Acknowledger != mutations {
		t.Fatalf("runtime activation binding = %d / %#v", attachments.bindCalls, attachments.bindRequest)
	}
}

func TestMutations_ActivateAndAbandonValidateClosedInputsAndCommitFailures(t *testing.T) {
	clock := time.Date(2026, time.August, 9, 12, 31, 0, 0, time.UTC)
	newMutations := func(store *mutationStore) *Mutations {
		mutations, err := NewMutations(MutationConfig{
			Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
			WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
			TaskIDs:            func(string) (string, error) { return "task-unused", nil },
			RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
			Clock: func() time.Time { return clock },
		})
		if err != nil {
			t.Fatal(err)
		}
		return mutations
	}
	validActivation := ActivateManagedRunCommand{
		OperationID: "operation-activate", ServiceInstanceID: "service-instance-0001",
		ManagedRunID: "managed-run-0001", ExternalRunRef: "task-0001",
		RegistrationNonce: "registration-nonce_0001", WorkspaceLeaseID: "workspace-lease-0001",
		ExecutionAttachmentID: "execution-attachment-0001",
		AttachmentTargetName:  "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
	}
	for _, test := range []struct {
		name   string
		mutate func(*ActivateManagedRunCommand)
	}{
		{name: "operation", mutate: func(command *ActivateManagedRunCommand) { command.OperationID = "bad id" }},
		{name: "service", mutate: func(command *ActivateManagedRunCommand) { command.ServiceInstanceID = "bad id" }},
		{name: "external ref", mutate: func(command *ActivateManagedRunCommand) { command.ExternalRunRef = "../task" }},
		{name: "nonce", mutate: func(command *ActivateManagedRunCommand) { command.RegistrationNonce = "short" }},
		{name: "binding", mutate: func(command *ActivateManagedRunCommand) { command.ManagedRunID = "" }},
		{name: "partial binding", mutate: func(command *ActivateManagedRunCommand) { command.WorkspaceLeaseID = ""; command.ManagedRunID = "" }},
	} {
		t.Run("activate "+test.name, func(t *testing.T) {
			command := validActivation
			test.mutate(&command)
			if _, err := newMutations(&mutationStore{}).ActivateManagedRun(context.Background(), command); err == nil {
				t.Fatal("ActivateManagedRun() error = nil")
			}
		})
	}
	//lint:ignore SA1012 The application boundary explicitly rejects a nil context.
	if _, err := newMutations(&mutationStore{}).ActivateManagedRun(nil, validActivation); err == nil {
		t.Fatal("ActivateManagedRun(nil) error = nil")
	}
	for _, test := range []struct {
		cause error
		code  domain.ErrorCode
	}{
		{cause: ErrInvalidInput, code: domain.ErrorInvalidArgument},
		{cause: ErrPrecondition, code: domain.ErrorPrecondition},
		{cause: ErrNotFound, code: domain.ErrorPrecondition},
		{cause: domain.ErrInvalidTransition, code: domain.ErrorPrecondition},
		{cause: ErrConflict, code: domain.ErrorConflict},
	} {
		store := &mutationStore{activationErr: test.cause}
		_, err := newMutations(store).ActivateManagedRun(context.Background(), validActivation)
		var failure *domain.Failure
		if !errors.As(err, &failure) || failure.Code != test.code {
			t.Fatalf("ActivateManagedRun(%v) error = %v, want %s", test.cause, err, test.code)
		}
	}
	privateFailure := errors.New("private activation store failure")
	if _, err := newMutations(&mutationStore{activationErr: privateFailure}).ActivateManagedRun(context.Background(), validActivation); !errors.Is(err, privateFailure) {
		t.Fatalf("ActivateManagedRun(private) error = %v", err)
	}

	validAbandon := AbandonManagedRunCommand{
		OperationID: "operation-abandon", ServiceInstanceID: "service-instance-0001",
		ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_0001",
		Reason: AbandonReasonOwnerCancelled, Disposition: AbandonDispositionPreserve,
	}
	store := &mutationStore{}
	if _, err := newMutations(store).AbandonManagedRun(context.Background(), validAbandon); err != nil {
		t.Fatal(err)
	}
	if store.abandon.ExternalRunRef != validAbandon.ExternalRunRef || store.abandon.Reason != validAbandon.Reason ||
		store.abandon.Disposition != validAbandon.Disposition || len(store.abandon.SubjectDigest) != 64 {
		t.Fatalf("abandon mutation = %#v", store.abandon)
	}
	for _, test := range []struct {
		name   string
		mutate func(*AbandonManagedRunCommand)
	}{
		{name: "operation", mutate: func(command *AbandonManagedRunCommand) { command.OperationID = "bad id" }},
		{name: "service", mutate: func(command *AbandonManagedRunCommand) { command.ServiceInstanceID = "bad id" }},
		{name: "external ref", mutate: func(command *AbandonManagedRunCommand) { command.ExternalRunRef = "../task" }},
		{name: "nonce", mutate: func(command *AbandonManagedRunCommand) { command.RegistrationNonce = "short" }},
		{name: "reason", mutate: func(command *AbandonManagedRunCommand) { command.Reason = "invented" }},
		{name: "disposition", mutate: func(command *AbandonManagedRunCommand) { command.Disposition = "invented" }},
	} {
		t.Run("abandon "+test.name, func(t *testing.T) {
			command := validAbandon
			test.mutate(&command)
			if _, err := newMutations(&mutationStore{}).AbandonManagedRun(context.Background(), command); err == nil {
				t.Fatal("AbandonManagedRun() error = nil")
			}
		})
	}
	//lint:ignore SA1012 The application boundary explicitly rejects a nil context.
	if _, err := newMutations(&mutationStore{}).AbandonManagedRun(nil, validAbandon); err == nil {
		t.Fatal("AbandonManagedRun(nil) error = nil")
	}
	_, err := newMutations(&mutationStore{abandonErr: ErrPrecondition}).AbandonManagedRun(context.Background(), validAbandon)
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != domain.ErrorPrecondition {
		t.Fatalf("AbandonManagedRun(precondition) error = %v", err)
	}
}

func TestMutations_StartTaskBuildsExactReplaySubject(t *testing.T) {
	clock := time.Date(2026, time.August, 9, 16, 10, 0, 0, time.UTC)
	store := &mutationStore{}
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-unused", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
		Clock: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	command := StartTaskCommand{OperationID: "op-start-0001", TaskHandle: "task-0001"}
	if _, err := mutations.StartTask(context.Background(), command); err != nil {
		t.Fatalf("StartTask() error = %v", err)
	}
	if store.start.TaskHandle != command.TaskHandle || len(store.start.SubjectDigest) != 64 || store.start.At != clock {
		t.Fatalf("start mutation = %#v, want exact task subject, SHA-256 digest, and injected time", store.start)
	}
}

func TestMutations_AcknowledgesExactProtectedLaunchAndRejectsInvalidEchoes(t *testing.T) {
	acknowledgement := LaunchAcknowledgement{
		TaskHandle: "task-launch-0001", ManagedRunID: "managed-run-launch-0001",
		WorkspaceLeaseID: "workspace-lease-launch-0001", WorkingDirectory: "/approved/workspaces/task-launch-0001",
		BriefRevision: 1, BriefRevisionHash: strings.Repeat("a", 64),
	}
	if err := acknowledgement.Validate(); err != nil {
		t.Fatalf("LaunchAcknowledgement.Validate() error = %v", err)
	}
	for _, mutate := range []func(*LaunchAcknowledgement){
		func(value *LaunchAcknowledgement) { value.TaskHandle = "../task" },
		func(value *LaunchAcknowledgement) { value.ManagedRunID = "bad id" },
		func(value *LaunchAcknowledgement) { value.WorkspaceLeaseID = "bad id" },
		func(value *LaunchAcknowledgement) { value.WorkingDirectory = "relative" },
		func(value *LaunchAcknowledgement) { value.BriefRevisionHash = "bad" },
	} {
		invalid := acknowledgement
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatalf("LaunchAcknowledgement.Validate(%#v) error = nil", invalid)
		}
	}
	if _, err := RuntimeLaunchAcknowledgementOperationID("../task"); err == nil {
		t.Fatal("RuntimeLaunchAcknowledgementOperationID(invalid) error = nil")
	}
	store := &mutationStore{}
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs: func(string) (string, error) { return "task-unused", nil }, RegistrationNonces: testRegistrationNonceSource,
		PreparationTTL: time.Hour, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := AcknowledgeWorkerLaunchCommand{OperationID: "operation-launch-ack", Acknowledgement: acknowledgement}
	if _, err := mutations.AcknowledgeWorkerLaunch(context.Background(), command); err != nil {
		t.Fatalf("AcknowledgeWorkerLaunch() error = %v", err)
	}
	if store.launchAck.Acknowledgement != acknowledgement || store.launchAck.OperationID != command.OperationID ||
		len(store.launchAck.SubjectDigest) != 64 {
		t.Fatalf("launch acknowledgement mutation = %#v", store.launchAck)
	}
	invalidCommand := command
	invalidCommand.OperationID = "bad id"
	if _, err := mutations.AcknowledgeWorkerLaunch(context.Background(), invalidCommand); err == nil {
		t.Fatal("AcknowledgeWorkerLaunch(invalid) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mutations.AcknowledgeWorkerLaunch(cancelled, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcknowledgeWorkerLaunch(cancelled) error = %v", err)
	}
}

func TestMutations_StartTaskRejectsInvalidCancelledAndAlteredReplay(t *testing.T) {
	store := &mutationStore{}
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-unused", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	for _, command := range []StartTaskCommand{
		{OperationID: "bad id", TaskHandle: "task-0001"},
		{OperationID: "op-start-0001", TaskHandle: "../escape"},
	} {
		if _, err := mutations.StartTask(context.Background(), command); err == nil {
			t.Fatalf("StartTask(%#v) error = nil", command)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mutations.StartTask(cancelled, StartTaskCommand{OperationID: "op-start-0001", TaskHandle: "task-0001"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartTask(cancelled) error = %v, want context.Canceled", err)
	}
	privateReplayError := errors.New("private replay failure")
	store.replayErr = privateReplayError
	if _, err := mutations.StartTask(context.Background(), StartTaskCommand{OperationID: "op-start-0001", TaskHandle: "task-0001"}); !errors.Is(err, privateReplayError) {
		t.Fatalf("StartTask(replay failure) error = %v, want preserved cause", err)
	}
	store.replayErr = nil
	store.replayFound = true
	store.replayResult = MutationResult{Task: domain.Task{Handle: "task-original"}}
	result, err := mutations.StartTask(context.Background(), StartTaskCommand{OperationID: "op-start-0001", TaskHandle: "task-0001"})
	if err != nil || result.Task.Handle != "task-original" {
		t.Fatalf("StartTask(replay) = %#v, %v, want original", result, err)
	}
}

func TestMutations_ClassifiesStableReplayConflictForEveryAdapter(t *testing.T) {
	store := &mutationStore{replayErr: fmt.Errorf("private store replay detail: %w", ErrConflict)}
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-unused", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
		Clock: func() time.Time { return time.Date(2026, time.August, 9, 12, 30, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mutations.PrepareTask(context.Background(), validPrepareCommand())
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != domain.ErrorConflict || failure.Retryable {
		t.Fatalf("PrepareTask(altered replay) error = %v, want nonretryable conflict", err)
	}
	if strings.Contains(err.Error(), "private store replay detail") {
		t.Fatalf("replay failure leaked private store detail: %v", err)
	}
}

func TestMutations_RejectsInvalidCommandsAndDependencyFailuresBeforeCommit(t *testing.T) {
	privateCause := errors.New("private repository cause")
	tests := []struct {
		name   string
		mutate func(*PrepareTaskCommand, *repositoryCatalog)
	}{
		{name: "operation", mutate: func(command *PrepareTaskCommand, _ *repositoryCatalog) { command.OperationID = "bad id" }},
		{name: "service instance", mutate: func(command *PrepareTaskCommand, _ *repositoryCatalog) { command.ServiceInstanceID = "bad id" }},
		{name: "shape", mutate: func(command *PrepareTaskCommand, _ *repositoryCatalog) {
			command.Shape = domain.TaskShape("initiative")
		}},
		{name: "repository", mutate: func(_ *PrepareTaskCommand, repositories *repositoryCatalog) { repositories.err = privateCause }},
		{name: "unsafe criterion", mutate: func(command *PrepareTaskCommand, _ *repositoryCatalog) {
			command.AcceptanceCriteria = []string{"unsafe\ncriterion"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &mutationStore{}
			repositories := &repositoryCatalog{}
			mutations, err := NewMutations(MutationConfig{
				Store: store, Repositories: repositories, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
				WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
				TaskIDs:            func(string) (string, error) { return "task-0001", nil },
				RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
				Clock: func() time.Time { return time.Date(2026, time.August, 9, 12, 30, 0, 0, time.UTC) },
			})
			if err != nil {
				t.Fatalf("NewMutations() error = %v", err)
			}
			command := validPrepareCommand()
			test.mutate(&command, repositories)
			_, err = mutations.PrepareTask(context.Background(), command)
			if err == nil {
				t.Fatal("PrepareTask() error = nil, want rejection")
			}
			if strings.Contains(err.Error(), privateCause.Error()) {
				t.Fatalf("safe mutation error leaked private cause: %q", err)
			}
			if store.prepareCalls != 0 {
				t.Fatalf("store prepare calls = %d, want zero", store.prepareCalls)
			}
		})
	}
}

func TestMutations_ValidatesRequiredDependenciesAndCancellation(t *testing.T) {
	valid := MutationConfig{
		Store: &mutationStore{}, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles:     func(string, domain.TaskShape) error { return nil },
		ValidationProfiles: func(string, domain.TaskShape) error { return nil },
		TaskIDs:            func(string) (string, error) { return "task-0001", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour, Clock: time.Now,
	}
	for _, mutate := range []func(*MutationConfig){
		func(config *MutationConfig) { config.Store = nil },
		func(config *MutationConfig) { config.Repositories = nil },
		func(config *MutationConfig) { config.Workspaces = nil },
		func(config *MutationConfig) { config.RuntimeAttachments = nil },
		func(config *MutationConfig) { config.TaskIDs = nil },
		func(config *MutationConfig) { config.RegistrationNonces = nil },
		func(config *MutationConfig) { config.PreparationTTL = 0 },
		func(config *MutationConfig) { config.PreparationTTL = 25 * time.Hour },
		func(config *MutationConfig) { config.Clock = nil },
		func(config *MutationConfig) { config.WorkerProfiles = nil },
		func(config *MutationConfig) { config.ValidationProfiles = nil },
		func(config *MutationConfig) {
			config.WorkerProfiles = nil
			config.ValidationProfiles = nil
		},
	} {
		config := valid
		mutate(&config)
		if _, err := NewMutations(config); err == nil {
			t.Fatal("NewMutations() error = nil for missing dependency")
		}
	}
	mutations, err := NewMutations(valid)
	if err != nil {
		t.Fatalf("NewMutations() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mutations.PrepareTask(cancelled, validPrepareCommand()); !errors.Is(err, context.Canceled) {
		t.Fatalf("PrepareTask(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestMutations_RejectsInvalidRegistrationIdentityWithoutCommit(t *testing.T) {
	clock := time.Date(2026, time.August, 9, 12, 30, 0, 0, time.UTC)
	privateCause := errors.New("private entropy failure")
	tests := []struct {
		name  string
		nonce RegistrationNonceSource
	}{
		{name: "source failure", nonce: func() (string, error) { return "", privateCause }},
		{name: "invalid shape", nonce: func() (string, error) { return "short", nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &mutationStore{}
			mutations, err := NewMutations(MutationConfig{
				Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
				WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
				TaskIDs:            func(string) (string, error) { return "task-0001", nil },
				RegistrationNonces: test.nonce, PreparationTTL: time.Hour,
				Clock: func() time.Time { return clock },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mutations.PrepareTask(context.Background(), validPrepareCommand()); err == nil {
				t.Fatal("PrepareTask() error = nil")
			} else if strings.Contains(err.Error(), privateCause.Error()) {
				t.Fatalf("private nonce error leaked: %v", err)
			}
			if store.prepareCalls != 0 {
				t.Fatalf("prepare calls = %d, want 0", store.prepareCalls)
			}
		})
	}
	if err := (ManagedRunPreparation{}).Validate(clock); err == nil {
		t.Fatal("ManagedRunPreparation.Validate(empty) error = nil")
	}
	if err := (ManagedRunPreparation{
		ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_0001",
		ExpiresAt: clock,
	}).Validate(clock); err == nil {
		t.Fatal("ManagedRunPreparation.Validate(expired) error = nil")
	}
	closedAt := clock.Add(time.Minute)
	validClosed := ManagedRunPreparation{
		ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_0001",
		RequestedWorkspaceRoot: "/approved/workspaces/task-0001",
		RequestedAttachment: PreparedRuntimeAttachment{
			Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/task-0001/attachment.sock",
			RelayIdentity: strings.Repeat("ab", 32),
		},
		ExpiresAt: clock.Add(time.Hour), State: PreparationAbandoned,
		AbandonReason: AbandonReasonOwnerCancelled, Disposition: AbandonDispositionPreserve,
		ClosedAt: &closedAt,
	}
	if err := validClosed.Validate(clock); err != nil {
		t.Fatalf("ManagedRunPreparation.Validate(closed) error = %v", err)
	}
	for _, mutate := range []func(*ManagedRunPreparation){
		func(preparation *ManagedRunPreparation) { preparation.RequestedWorkspaceRoot = "relative/workspace" },
		func(preparation *ManagedRunPreparation) { preparation.RequestedAttachment.RelayIdentity = "bad" },
		func(preparation *ManagedRunPreparation) { preparation.State = "invented" },
		func(preparation *ManagedRunPreparation) { preparation.AbandonReason = "invented" },
		func(preparation *ManagedRunPreparation) { preparation.Disposition = "invented" },
		func(preparation *ManagedRunPreparation) { preparation.ClosedAt = nil },
	} {
		invalid := validClosed
		mutate(&invalid)
		if err := invalid.Validate(clock); err == nil {
			t.Fatalf("ManagedRunPreparation.Validate(%#v) error = nil", invalid)
		}
	}
}

func testRuntimeAttachments() *runtimeAttachmentCoordinator {
	return &runtimeAttachmentCoordinator{prepared: PreparedRuntimeAttachment{
		Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/task-0001/attachment.sock",
		RelayIdentity: strings.Repeat("ab", 32),
	}}
}

type mutationStore struct {
	intent          TaskPreparationIntent
	prepared        PreparedTaskMutation
	activation      ManagedRunActivationMutation
	abandon         ManagedRunAbandonMutation
	start           TaskStartMutation
	terminal        TerminalEventMutation
	launchAck       WorkerLaunchAcknowledgementMutation
	pauseRequest    TaskPauseRequestMutation
	cancelTask      TaskCancelMutation
	verifyTask      TaskVerifyMutation
	steerTask       TaskSteerMutation
	cancelDecision  DecisionCancellationMutation
	respondDecision DecisionResponseMutation
	respondErr      error
	prepareCalls    int
	replayResult    MutationResult
	replayFound     bool
	replayErr       error
	activationErr   error
	abandonErr      error
}

func (store *mutationStore) RecordTaskPreparationIntent(
	_ context.Context,
	intent TaskPreparationIntent,
) (TaskPreparationIntent, error) {
	if store.intent.OperationID != "" {
		return store.intent, nil
	}
	store.intent = intent
	return intent, nil
}

func (store *mutationStore) ReplayMutation(context.Context, string, string, string) (MutationResult, bool, error) {
	return store.replayResult, store.replayFound, store.replayErr
}

func (store *mutationStore) CommitPreparedTask(_ context.Context, mutation PreparedTaskMutation) (MutationResult, error) {
	store.prepareCalls++
	store.prepared = mutation
	// The real store returns the task it persisted. Echoing it here keeps every
	// caller that reads the prepared task from the result — scout promotion
	// links the ship handle it gets back — testable against what was committed.
	return MutationResult{
		Task:        mutation.Task,
		Preparation: &mutation.Preparation,
		Operation:   domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitManagedRunActivation(_ context.Context, mutation ManagedRunActivationMutation) (MutationResult, error) {
	store.activation = mutation
	return MutationResult{}, store.activationErr
}

func (store *mutationStore) CommitManagedRunAbandon(_ context.Context, mutation ManagedRunAbandonMutation) (MutationResult, error) {
	store.abandon = mutation
	return MutationResult{}, store.abandonErr
}

func (store *mutationStore) CommitTaskStart(_ context.Context, mutation TaskStartMutation) (MutationResult, error) {
	store.start = mutation
	return MutationResult{}, nil
}

func (store *mutationStore) CommitTerminalEvent(_ context.Context, mutation TerminalEventMutation) (MutationResult, error) {
	store.terminal = mutation
	return MutationResult{}, nil
}

func (store *mutationStore) CommitWorkerLaunchAcknowledgement(_ context.Context, mutation WorkerLaunchAcknowledgementMutation) (MutationResult, error) {
	store.launchAck = mutation
	return MutationResult{}, nil
}

type repositoryCatalog struct {
	repositoryID string
	calls        int
	err          error
}

func (catalog *repositoryCatalog) ValidateRepository(_ context.Context, repositoryID string) error {
	catalog.calls++
	catalog.repositoryID = repositoryID
	return catalog.err
}

func validPrepareCommand() PrepareTaskCommand {
	return PrepareTaskCommand{
		OperationID: "op-prepare-0001", ServiceInstanceID: "service-instance-0001",
		Shape: domain.ShapeShip, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"The requested behavior is proven."}, Constraints: []string{"Preserve unrelated changes."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "fixture-worker",
	}
}

func acceptingWorkerProfile(string, domain.TaskShape) error { return nil }

func acceptingValidationProfile(string, domain.TaskShape) error { return nil }

func testRegistrationNonceSource() (string, error) { return "registration-nonce_0001", nil }

func testWorkspacePreparer() *workspacePreparer {
	return &workspacePreparer{prepared: PreparedWorkspace{CanonicalRoot: "/approved/workspaces/task-0001"}}
}

// Cancellation joins the same durable mutation surface; the double accepts it.
func (store *mutationStore) CommitDecisionResponse(
	_ context.Context,
	mutation DecisionResponseMutation,
) (MutationResult, error) {
	store.respondDecision = mutation
	if store.respondErr != nil {
		return MutationResult{}, store.respondErr
	}
	return MutationResult{
		Task:      domain.Task{Handle: mutation.TaskHandle, State: domain.TaskWorking},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitDecisionCancellation(
	_ context.Context,
	mutation DecisionCancellationMutation,
) (MutationResult, error) {
	store.cancelDecision = mutation
	return MutationResult{
		Task:      domain.Task{Handle: mutation.TaskHandle, State: domain.TaskWorking},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitTaskSteer(
	_ context.Context,
	mutation TaskSteerMutation,
) (MutationResult, error) {
	store.steerTask = mutation
	return MutationResult{
		Task:      domain.Task{Handle: mutation.TaskHandle, State: domain.TaskWorking},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitTaskVerify(
	_ context.Context,
	mutation TaskVerifyMutation,
) (MutationResult, error) {
	store.verifyTask = mutation
	return MutationResult{
		Task:      domain.Task{Handle: mutation.TaskHandle, State: domain.TaskValidating},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitTaskCancel(
	_ context.Context,
	mutation TaskCancelMutation,
) (MutationResult, error) {
	store.cancelTask = mutation
	return MutationResult{
		Task:      domain.Task{Handle: mutation.TaskHandle, State: domain.TaskCancelled},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitTaskPauseRequest(
	_ context.Context,
	mutation TaskPauseRequestMutation,
) (MutationResult, error) {
	store.pauseRequest = mutation
	return MutationResult{
		Task:      domain.Task{Handle: mutation.TaskHandle, State: domain.TaskWorking},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func (store *mutationStore) CommitManagedRunCancel(
	_ context.Context,
	mutation ManagedRunCancelMutation,
) (MutationResult, error) {
	return MutationResult{
		Task:      domain.Task{Handle: "task_cancel", State: domain.TaskCancelled},
		Operation: domain.OperationRecord{ID: mutation.OperationID},
	}, nil
}

func TestMutations_CancelValidatesClosedInputsBeforeTouchingTheStore(t *testing.T) {
	clock := time.Date(2026, time.August, 9, 12, 31, 0, 0, time.UTC)
	newMutations := func(store *mutationStore) *Mutations {
		mutations, err := NewMutations(MutationConfig{
			Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(),
			RuntimeAttachments: testRuntimeAttachments(),
			WorkerProfiles:     acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
			TaskIDs:            func(string) (string, error) { return "task-unused", nil },
			RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
			Clock: func() time.Time { return clock },
		})
		if err != nil {
			t.Fatal(err)
		}
		return mutations
	}
	valid := CancelManagedRunCommand{
		OperationID: "operation-cancel", ServiceInstanceID: "service-instance-0001",
		ManagedRunID: "managed-run-0001", Reason: CancelReasonOwnerCancelled,
	}
	if _, err := newMutations(&mutationStore{}).CancelManagedRun(context.Background(), valid); err != nil {
		t.Fatalf("CancelManagedRun(valid) error = %v", err)
	}
	for name, mutate := range map[string]func(*CancelManagedRunCommand){
		"operation":  func(c *CancelManagedRunCommand) { c.OperationID = "" },
		"instance":   func(c *CancelManagedRunCommand) { c.ServiceInstanceID = "" },
		"managedRun": func(c *CancelManagedRunCommand) { c.ManagedRunID = "" },
		// A reason outside the closed set is refused rather than recorded: the
		// audit trail for a stopped run must name a reason the host defined.
		"reason": func(c *CancelManagedRunCommand) { c.Reason = CancelReason("because") },
	} {
		command := valid
		mutate(&command)
		if _, err := newMutations(&mutationStore{}).CancelManagedRun(context.Background(), command); err == nil {
			t.Errorf("%s: CancelManagedRun accepted invalid input", name)
		}
	}
}

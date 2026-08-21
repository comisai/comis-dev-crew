package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestStartTaskConfigurationRejectsInvalidSchedulingLimits(t *testing.T) {
	config := MutationConfig{
		Store: &mutationStore{}, Repositories: &repositoryCatalog{},
		WorkerProfiles: acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		Workspaces: testWorkspacePreparer(), RuntimeAttachments: testRuntimeAttachments(),
		TaskIDs:            func(string) (string, error) { return "task-unused", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
		SchedulingLimits: &InitiativeSchedulingLimits{
			MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 2,
			WorkerProfileLimits: map[string]int{"codex-reviewed": 1},
		},
		Clock: time.Now,
	}
	if _, err := NewMutations(config); err == nil {
		t.Fatal("NewMutations(repository ceiling above host ceiling) error = nil")
	}
}

func TestStartTaskClassifiesResourceQueueAsPrecondition(t *testing.T) {
	store := &mutationStore{startErr: fmt.Errorf("resource_queued: %w", ErrPrecondition)}
	_, err := newTestMutations(t, store).StartTask(context.Background(), StartTaskCommand{
		OperationID: "operation-start-resource-queued", TaskHandle: "task-resource-queued",
	})
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != domain.ErrorPrecondition || !errors.Is(err, ErrPrecondition) {
		t.Fatalf("StartTask() error = %v, want typed precondition preserving the application cause", err)
	}
}

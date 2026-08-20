package application

import (
	"testing"
	"time"
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

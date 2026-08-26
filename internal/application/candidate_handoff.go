package application

import "github.com/comisai/comis-dev-crew/internal/domain"

// CandidateHandoffAuthority is the durable preparation identity required to
// promote an accepted worker candidate into the service-owned task branch.
// Terminal settlement is deliberately absent: it belongs only to recovery of
// an unknown task that has no accepted candidate report.
type CandidateHandoffAuthority struct {
	Task                   domain.Task
	Preparation            ManagedRunPreparation
	PreparationOperationID string
}

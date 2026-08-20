package localapi

import "github.com/comisai/comis-dev-crew/internal/application"

func projectedStateVersion(result any) (int64, bool) {
	switch projection := result.(type) {
	case *application.DiagnosticReport:
		return projection.StateVersion, true
	case *application.FleetSnapshot:
		return projection.StateVersion, true
	case *application.TaskList:
		return projection.StateVersion, true
	case *application.InitiativeList:
		return projection.StateVersion, true
	case *application.InitiativeDetail:
		return projection.StateVersion, true
	case *application.BacklogList:
		return projection.StateVersion, true
	case *application.WorkerProfileList:
		return projection.StateVersion, true
	case *application.TaskDetail:
		return projection.StateVersion, true
	case *application.TaskDiffView:
		return projection.StateVersion, true
	case *application.RepairSurvey:
		return projection.StateVersion, true
	case *application.TaskLogPage:
		return projection.NextCursor, true
	case *application.EventPage:
		return projection.NextCursor, true
	case *application.AuditPage:
		return projection.NextCursor, true
	case *application.DecisionList:
		return projection.StateVersion, true
	case *application.TaskDecision:
		return projection.StateVersion, true
	case *application.TaskExplanation:
		return projection.Summary.StateVersion, true
	case *application.LaunchPlan:
		return projection.StateVersion, true
	case *application.OperationView:
		return projection.StateVersion, true
	case *application.PrimarySyncReport:
		return projection.StateVersion, true
	case *PrepareTaskResult:
		return projection.StateVersion, true
	case *PrepareInitiativeResult:
		return projection.StateVersion, true
	case *AddBacklogResult:
		return projection.StateVersion, true
	case *PromoteBacklogResult:
		return projection.StateVersion, true
	case *InitiativeControlResult:
		return projection.StateVersion, true
	case *TaskMutationResult:
		return projection.StateVersion, true
	default:
		return 0, false
	}
}

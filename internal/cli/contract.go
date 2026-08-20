package cli

import (
	"context"
	"io"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

const usage = `Usage: devcrew [--socket PATH] <command>

Commands:
  service status
  doctor [--format table|json]
  status [--watch [--passes N] [--interval DURATION]] [--format table|json]
  tasks list [--state STATE] [--format table|json]
  workers list [--format table|json]
  initiative list [--state STATE] [--format table|json]
  initiative show INITIATIVE [--format text|json]
  initiative explain INITIATIVE [--format text|json]
  initiative graph INITIATIVE [--format text|json]
  initiative watch INITIATIVE [--passes N] [--interval DURATION]
  initiative pause INITIATIVE [--operation OPERATION] [--format json]
  initiative resume INITIATIVE [--operation OPERATION] [--format json]
  initiative cancel INITIATIVE [--operation OPERATION] [--format json]
  task show TASK [--format yaml|json]
  task explain TASK [--format text|json]
  task diff TASK [--stat|--name-only] [--format text|json]
  task logs TASK [--source worker|service|validation] [--follow [--passes N]] [--format text|json]
  task launch-plan TASK [--format json]
  task operation OPERATION [--format text|json]
  task prepare --input FILE|- [--operation OPERATION] [--format json]
  task reconcile TASK --action validate-clean-candidate [--operation OPERATION] [--format json]
  task handback TASK --action validate-developer-work [--operation OPERATION] [--format json]
  task pause TASK [--operation OPERATION] [--format json]
  task cancel TASK [--operation OPERATION] [--format json]
  task resume TASK [--operation OPERATION] [--format json]
  task verify TASK [--operation OPERATION] [--format json]
  task attest SCOUT --finding open_decisions|no_open_decisions [--open-decision KEY ...] [--operation OPERATION] [--format json]
  task promote SCOUT --input FILE|- [--operation OPERATION] [--format json]
  task replace TASK --worker PROFILE [--operation OPERATION] [--format json]
  task steer TASK --input FILE|- [--operation OPERATION] [--format json]
  task cleanup TASK [--operation OPERATION] [--format json]
  task discard TASK --yes [--operation OPERATION] [--format json]
  events tail [--after SEQUENCE] [--task TASK] [--format text|jsonl]
  audit tail [--after SEQUENCE] [--format text|jsonl]
  repair reconcile [--task TASK] [--format table|json]
  decisions list [--task TASK] [--format table|json]
  decision show TASK DECISION [--format text|json]
  decision respond TASK DECISION --input FILE|- [--operation OPERATION] [--format json]
  decision cancel TASK DECISION [--operation OPERATION] [--format json]

Global options:
  --socket PATH  Owner-only service Unix socket
  --help, -h     Show this help
  --version      Show version
`

// ReadClient is the canonical query client consumed by the CLI adapter.
type ReadClient interface {
	Diagnose(context.Context, string) (application.DiagnosticReport, error)
	Fleet(context.Context, string) (application.FleetSnapshot, error)
	ListTasks(context.Context, string, localapi.ListTasksInput) (application.TaskList, error)
	ListWorkerProfiles(context.Context, string) (application.WorkerProfileList, error)
	ListInitiatives(context.Context, string, localapi.ListInitiativesInput) (application.InitiativeList, error)
	GetInitiative(context.Context, string, string) (application.InitiativeDetail, error)
	PauseInitiative(context.Context, string, localapi.InitiativeControlInput) (localapi.InitiativeControlResult, error)
	ResumeInitiative(context.Context, string, localapi.InitiativeControlInput) (localapi.InitiativeControlResult, error)
	CancelInitiative(context.Context, string, localapi.InitiativeControlInput) (localapi.InitiativeControlResult, error)
	PauseTask(context.Context, string, localapi.PauseTaskInput) (localapi.TaskMutationResult, error)
	CancelTask(context.Context, string, localapi.CancelTaskInput) (localapi.TaskMutationResult, error)
	ResumeTask(context.Context, string, localapi.ResumeTaskInput) (localapi.TaskMutationResult, error)
	VerifyTask(context.Context, string, localapi.VerifyTaskInput) (localapi.TaskMutationResult, error)
	PromoteScout(context.Context, string, localapi.PromoteScoutInput) (localapi.PrepareTaskResult, error)
	ReplaceWorker(context.Context, string, localapi.ReplaceWorkerInput) (localapi.TaskMutationResult, error)
	SteerTask(context.Context, string, localapi.SteerTaskInput) (localapi.TaskMutationResult, error)
	AttestScoutDecisions(context.Context, string, localapi.AttestScoutDecisionsInput) (localapi.TaskMutationResult, error)
	DiscardTask(context.Context, string, localapi.DiscardTaskInput) (localapi.TaskMutationResult, error)
	DiffTask(context.Context, string, string) (application.TaskDiffView, error)
	SurveyRepairs(context.Context, string, localapi.SurveyRepairsInput) (application.RepairSurvey, error)
	ReadEvents(context.Context, string, localapi.ReadEventsInput) (application.EventPage, error)
	ReadAudit(context.Context, string, localapi.ReadAuditInput) (application.AuditPage, error)
	ReadTaskLogs(context.Context, string, localapi.ReadTaskLogsInput) (application.TaskLogPage, error)
	ListDecisions(context.Context, string, localapi.ListDecisionsInput) (application.DecisionList, error)
	ShowDecision(context.Context, string, localapi.ShowDecisionInput) (application.TaskDecision, error)
	CancelDecision(context.Context, string, localapi.CancelDecisionInput) (localapi.TaskMutationResult, error)
	RespondDecision(context.Context, string, localapi.RespondDecisionInput) (localapi.TaskMutationResult, error)
	ShowTask(context.Context, string, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string, string) (application.TaskExplanation, error)
	GetLaunchPlan(context.Context, string, string) (application.LaunchPlan, error)
	Operation(context.Context, string, string) (application.OperationView, error)
	PrepareTask(context.Context, string, localapi.PrepareTaskInput) (localapi.PrepareTaskResult, error)
	ReconcileTask(context.Context, string, localapi.ReconcileTaskInput) (localapi.TaskMutationResult, error)
	HandbackTask(context.Context, string, localapi.HandbackTaskInput) (localapi.TaskMutationResult, error)
	CleanupTask(context.Context, string, localapi.CleanupTaskInput) (localapi.TaskMutationResult, error)
}

// Config injects host paths, client creation, and operation identity.
type Config struct {
	DefaultSocketPath string
	Version           string
	NewClient         func(string) (ReadClient, error)
	NewOperationID    func() (string, error)
	Stdin             io.Reader
	// Sleep paces watch passes. It is injected so a watch can be driven without
	// wall-clock delay.
	Sleep     func(time.Duration)
	OpenInput func(string) (io.ReadCloser, error)
}

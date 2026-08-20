package cli

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

// execute maps one parsed command onto exactly one client call. Every command
// appears here once and nowhere else, so a surface cannot grow a second way to
// reach the same service operation.
func execute(ctx context.Context, client ReadClient, operationID string, command parsedCommand) (any, error) {
	switch command.kind {
	case commandServiceStatus, commandDoctor:
		return client.Diagnose(ctx, operationID)
	case commandFleet:
		return client.Fleet(ctx, operationID)
	case commandListTasks:
		return client.ListTasks(ctx, operationID, localapi.ListTasksInput{State: domain.TaskState(command.taskState)})
	case commandWorkerProfiles:
		return client.ListWorkerProfiles(ctx, operationID)
	case commandListInitiatives:
		return client.ListInitiatives(ctx, operationID, localapi.ListInitiativesInput{
			State: domain.InitiativeState(command.initiativeState),
		})
	case commandShowInitiative, commandExplainInitiative:
		return client.GetInitiative(ctx, operationID, command.reference)
	case commandGraphInitiative:
		detail, err := client.GetInitiative(ctx, operationID, command.reference)
		return detail.Graph, err
	case commandWatchInitiative:
		page, err := client.ReadEvents(ctx, initiativeWatchOperationID(operationID), localapi.ReadEventsInput{
			AfterSequence: command.eventCursor,
		})
		if err != nil {
			return nil, err
		}
		detail, err := client.GetInitiative(ctx, operationID, command.reference)
		return initiativeWatchResult{Detail: detail, NextCursor: page.NextCursor}, err
	case commandPauseInitiative:
		return client.PauseInitiative(ctx, operationID, localapi.InitiativeControlInput{InitiativeHandle: command.reference})
	case commandResumeInitiative:
		return client.ResumeInitiative(ctx, operationID, localapi.InitiativeControlInput{InitiativeHandle: command.reference})
	case commandCancelInitiative:
		return client.CancelInitiative(ctx, operationID, localapi.InitiativeControlInput{InitiativeHandle: command.reference})
	case commandAddBacklog:
		if command.backlogAddInput == nil {
			return nil, errors.New("backlog addition input is unavailable")
		}
		return client.AddBacklog(ctx, operationID, *command.backlogAddInput)
	case commandPromoteBacklog:
		if command.backlogPromoteInput == nil {
			return nil, errors.New("backlog promotion input is unavailable")
		}
		return client.PromoteBacklog(ctx, operationID, *command.backlogPromoteInput)
	case commandReadTaskLogs:
		return client.ReadTaskLogs(ctx, operationID, localapi.ReadTaskLogsInput{
			TaskHandle: command.reference, Source: command.logSource, AfterSequence: command.logCursor,
		})
	case commandReadEvents:
		return client.ReadEvents(ctx, operationID, localapi.ReadEventsInput{AfterSequence: command.eventCursor, TaskHandle: command.reference})
	case commandReadAudit:
		return client.ReadAudit(ctx, operationID, localapi.ReadAuditInput{AfterSequence: command.eventCursor})
	case commandSurveyRepairs:
		return client.SurveyRepairs(ctx, operationID, localapi.SurveyRepairsInput{TaskHandle: command.reference})
	case commandDiffTask:
		return client.DiffTask(ctx, operationID, command.reference)
	case commandListDecisions:
		return client.ListDecisions(ctx, operationID, localapi.ListDecisionsInput{TaskHandle: command.reference})
	case commandCancelDecision:
		return client.CancelDecision(ctx, operationID, localapi.CancelDecisionInput{
			TaskHandle: command.reference, ExternalKey: command.decisionKey,
		})
	case commandRespondDecision:
		return client.RespondDecision(ctx, operationID, localapi.RespondDecisionInput{
			TaskHandle: command.reference, ExternalKey: command.decisionKey, Response: command.decisionAnswer,
		})
	case commandShowDecision:
		return client.ShowDecision(ctx, operationID, localapi.ShowDecisionInput{
			TaskHandle: command.reference, ExternalKey: command.decisionKey,
		})
	case commandShowTask:
		return client.ShowTask(ctx, operationID, command.reference)
	case commandExplainTask:
		return client.ExplainTask(ctx, operationID, command.reference)
	case commandGetLaunchPlan:
		return client.GetLaunchPlan(ctx, operationID, command.reference)
	case commandOperation:
		return client.Operation(ctx, operationID, command.reference)
	case commandPrepareTask:
		if command.prepareInput == nil {
			return nil, errors.New("prepare input is unavailable")
		}
		return client.PrepareTask(ctx, operationID, *command.prepareInput)
	case commandPromoteScout:
		if command.promoteInput == nil {
			return nil, errors.New("promotion input is unavailable")
		}
		return client.PromoteScout(ctx, operationID, *command.promoteInput)
	case commandReconcileTask:
		return client.ReconcileTask(ctx, operationID, localapi.ReconcileTaskInput{
			TaskHandle: command.reference, Action: command.reconcileAction,
		})
	case commandHandbackTask:
		return client.HandbackTask(ctx, operationID, localapi.HandbackTaskInput{
			TaskHandle: command.reference, Action: command.handbackAction,
		})
	case commandDiscardTask:
		return client.DiscardTask(ctx, operationID, localapi.DiscardTaskInput{
			TaskHandle: command.reference, Acknowledged: command.acknowledged,
		})
	case commandCleanupTask:
		return client.CleanupTask(ctx, operationID, localapi.CleanupTaskInput{TaskHandle: command.reference})
	case commandPauseTask:
		return client.PauseTask(ctx, operationID, localapi.PauseTaskInput{TaskHandle: command.reference})
	case commandCancelTask:
		return client.CancelTask(ctx, operationID, localapi.CancelTaskInput{TaskHandle: command.reference})
	case commandResumeTask:
		return client.ResumeTask(ctx, operationID, localapi.ResumeTaskInput{TaskHandle: command.reference})
	case commandVerifyTask:
		return client.VerifyTask(ctx, operationID, localapi.VerifyTaskInput{TaskHandle: command.reference})
	case commandSteerTask:
		return client.SteerTask(ctx, operationID, localapi.SteerTaskInput{
			TaskHandle: command.reference, Instruction: command.instruction,
		})
	case commandAttestScout:
		return client.AttestScoutDecisions(ctx, operationID, localapi.AttestScoutDecisionsInput{
			TaskHandle: command.reference, Finding: command.attestFinding,
			OpenDecisionKeys: append([]string(nil), command.attestKeys...),
		})
	case commandReplaceWorker:
		return client.ReplaceWorker(ctx, operationID, localapi.ReplaceWorkerInput{
			TaskHandle: command.reference, WorkerProfileID: command.workerProfileID,
		})
	default:
		return nil, errors.New("unknown parsed command")
	}
}

type initiativeWatchResult struct {
	Detail     application.InitiativeDetail
	NextCursor int64
}

func initiativeWatchOperationID(operationID string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(operationID+"\x00initiative-events")))
	return "watch-events-" + digest[:32]
}

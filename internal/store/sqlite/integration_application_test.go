package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestIntegrationApplicationPersistsEveryClosedOutcomeAcrossRestart(t *testing.T) {
	for _, outcome := range []application.IntegrationOutcome{
		application.IntegrationApplied,
		application.IntegrationConflicted,
		application.IntegrationInvalidated,
		application.IntegrationAborted,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			request := fixture.reservationRequest("integration-store-"+string(outcome), application.IntegrationCherryPick)
			reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
			if err != nil {
				t.Fatalf("ReserveIntegrationApplication() error = %v", err)
			}
			if reserved.Result != nil || reserved.Candidate.EvidenceDigest != fixture.evidenceDigest ||
				reserved.Target.TaskHandle != "task-integration" || reserved.Candidate.TaskHandle != "task-component-a" ||
				domain.ValidateOperationID(reserved.Target.PreparationOperationID) != nil ||
				!reserved.EvidenceExpiresAt.Equal(fixture.evidenceExpiresAt) {
				t.Fatalf("reservation = %#v", reserved)
			}
			adapterResult := application.IntegrationAdapterResult{
				Outcome: outcome, PreviousHead: request.Command.ExpectedIntegrationHead,
			}
			if outcome == application.IntegrationApplied {
				adapterResult.ResultingHead = strings.Repeat("d", 40)
			} else if outcome == application.IntegrationConflicted {
				adapterResult.ConflictPaths = []string{"internal/api.go", "web/client.ts"}
			}
			completedAt := request.At.Add(time.Second)
			completed, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
				Reservation: reserved, AdapterResult: adapterResult, At: completedAt,
			})
			if err != nil {
				t.Fatalf("CompleteIntegrationApplication() error = %v", err)
			}
			if completed.Outcome != outcome || completed.StateVersion < 1 || !completed.CompletedAt.Equal(completedAt) {
				t.Fatalf("completed = %#v", completed)
			}
			if outcome == application.IntegrationInvalidated {
				candidate, readErr := fixture.store.GetTask(context.Background(), reserved.Candidate.TaskHandle)
				if readErr != nil || candidate.State != domain.TaskValidating {
					t.Fatalf("invalidated candidate = %#v, %v", candidate, readErr)
				}
			}
			operation, err := fixture.store.GetOperation(context.Background(), request.Command.OperationID)
			if err != nil || operation.Command != "ApplyIntegrationCandidate" ||
				operation.SubjectDigest != request.SubjectDigest || operation.StateVersion != completed.StateVersion {
				t.Fatalf("operation = %#v, %v", operation, err)
			}
			if err := fixture.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(context.Background(), fixture.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			replayed, err := reopened.ReserveIntegrationApplication(context.Background(), request)
			if err != nil || replayed.Result == nil || !reflect.DeepEqual(*replayed.Result, completed) {
				t.Fatalf("ReserveIntegrationApplication(restart) = %#v, %v", replayed, err)
			}
			recompleted, err := reopened.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
				Reservation: replayed, AdapterResult: adapterResult, At: completedAt,
			})
			if err != nil || !reflect.DeepEqual(recompleted, completed) {
				t.Fatalf("CompleteIntegrationApplication(replay) = %#v, %v", recompleted, err)
			}
		})
	}
}

func TestIntegrationAbortedSettlementPreservesCandidateEvidence(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-store-aborted", application.IntegrationRebase)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	aborted := application.IntegrationOutcome("aborted")
	completed, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: aborted, PreviousHead: request.Command.ExpectedIntegrationHead,
		},
		At: request.At.Add(time.Second),
	})
	if err != nil || completed.Outcome != aborted {
		t.Fatalf("CompleteIntegrationApplication(aborted) = %#v, %v", completed, err)
	}
	candidate, err := fixture.store.GetTask(context.Background(), reserved.Candidate.TaskHandle)
	if err != nil || candidate.State != domain.TaskCandidateComplete {
		t.Fatalf("aborted candidate = %#v, %v", candidate, err)
	}
}

func TestIntegrationReservationSurvivesRestartBeforeGitCompletion(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-reserved-restart", application.IntegrationMerge)
	first, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil || first.Result != nil {
		t.Fatalf("ReserveIntegrationApplication() = %#v, %v", first, err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replayed, err := reopened.ReserveIntegrationApplication(context.Background(), request)
	if err != nil || !reflect.DeepEqual(replayed, first) || replayed.Result != nil {
		t.Fatalf("ReserveIntegrationApplication(restart) = %#v, %v", replayed, err)
	}
	altered := request
	altered.SubjectDigest = strings.Repeat("f", 64)
	if _, err := reopened.ReserveIntegrationApplication(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReserveIntegrationApplication(altered replay) error = %v", err)
	}
}

func TestIntegrationPreparationIdentityMigrationBackfillsReservedRows(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-preparation-upgrade", application.IntegrationMerge)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	wantPreparation := reserved.Target.PreparationOperationID
	if _, err := fixture.store.db.Exec(`ALTER TABLE integration_applications
		DROP COLUMN target_preparation_operation_id;
		DELETE FROM schema_migrations WHERE version = 46`); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), fixture.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replayed, err := reopened.ReserveIntegrationApplication(context.Background(), request)
	if err != nil || replayed.Target.PreparationOperationID != wantPreparation {
		t.Fatalf("ReserveIntegrationApplication(upgraded replay) = %#v, %v", replayed, err)
	}
}

func TestReservedIntegrationBlocksCancellationOfEitherBoundTask(t *testing.T) {
	for _, taskHandle := range []string{"task-integration", "task-component-a"} {
		t.Run(taskHandle, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			request := fixture.reservationRequest("integration-cancel-order-"+taskHandle, application.IntegrationMerge)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err != nil {
				t.Fatalf("ReserveIntegrationApplication() error = %v", err)
			}
			before, err := fixture.store.GetTask(context.Background(), taskHandle)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fixture.store.CommitTaskCancel(context.Background(), cancelTaskMutation(
				taskHandle, "cancel-reserved-"+taskHandle, request.At.Add(time.Minute),
			))
			if !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("CommitTaskCancel(reserved integration) error = %v", err)
			}
			_, err = fixture.store.CommitManagedRunCancel(context.Background(), application.ManagedRunCancelMutation{
				ServiceInstanceID: before.ServiceInstanceID, ManagedRunID: before.ManagedRunID,
				Reason: application.CancelReasonOwnerCancelled, OperationID: "managed-cancel-reserved-" + taskHandle,
				SubjectDigest: strings.Repeat("8", 64), At: request.At.Add(2 * time.Minute),
			})
			if !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("CommitManagedRunCancel(reserved integration) error = %v", err)
			}
			after, err := fixture.store.GetTask(context.Background(), taskHandle)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("task after refused cancel = %#v, %v; want %#v", after, err, before)
			}
		})
	}
}

func TestReservedIntegrationBlocksAuthorityInvalidatingWorkerReport(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-report-order", application.IntegrationMerge)
	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	before, err := fixture.store.GetTask(context.Background(), "task-integration")
	if err != nil {
		t.Fatal(err)
	}
	report := sqliteWorkerReport(before, "report-reserved-integration-paused", domain.ReportPaused)
	if _, err := fixture.store.CommitReport(
		context.Background(), directReportMutation(before, report, request.At.Add(time.Minute)),
	); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitReport(reserved integration pause) error = %v", err)
	}
	after, err := fixture.store.GetTask(context.Background(), before.Handle)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("task after refused report = %#v, %v; want %#v", after, err, before)
	}
}

func TestIntegrationRebaseConflictRecoveryIsASeparateDurableOperation(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	initialRequest := fixture.reservationRequest("integration-rebase-conflict-store", application.IntegrationRebase)
	initial, err := fixture.store.ReserveIntegrationApplication(context.Background(), initialRequest)
	if err != nil {
		t.Fatal(err)
	}
	conflicted, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: initial,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationConflicted, PreviousHead: initial.Target.ExpectedHead,
			ConflictPaths: []string{"fixture.txt"},
		},
		At: initialRequest.At.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveryRequest := fixture.reservationRequest("integration-rebase-recovery-store", application.IntegrationRebase)
	recoveryRequest.Command.RecoveryOperationID = initial.OperationID
	recoveryRequest.SubjectDigest = strings.Repeat("8", 64)
	recoveryRequest.At = fixture.evidenceExpiresAt.Add(time.Hour)
	recovery, err := fixture.store.ReserveIntegrationApplication(context.Background(), recoveryRequest)
	if err != nil {
		t.Fatalf("ReserveIntegrationApplication(recovery) error = %v", err)
	}
	if recovery.OperationID != recoveryRequest.Command.OperationID ||
		recovery.RecoveryOperationID != initial.OperationID || recovery.Result != nil ||
		!recovery.ReservedAt.Equal(recoveryRequest.At) {
		t.Fatalf("recovery reservation = %#v", recovery)
	}
	resolved, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: recovery,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationApplied, PreviousHead: recovery.Target.ExpectedHead,
			ResultingHead: strings.Repeat("d", 40),
		},
		At: recoveryRequest.At,
	})
	if err != nil || resolved.RecoveryOperationID != initial.OperationID || resolved.Outcome != application.IntegrationApplied {
		t.Fatalf("CompleteIntegrationApplication(recovery) = %#v, %v", resolved, err)
	}
	initialReplay, err := fixture.store.ReserveIntegrationApplication(context.Background(), initialRequest)
	if err != nil || initialReplay.Result == nil || !reflect.DeepEqual(*initialReplay.Result, conflicted) {
		t.Fatalf("initial conflict replay = %#v, %v", initialReplay, err)
	}
	recoveryReplay, err := fixture.store.ReserveIntegrationApplication(context.Background(), recoveryRequest)
	if err != nil || recoveryReplay.Result == nil || !reflect.DeepEqual(*recoveryReplay.Result, resolved) {
		t.Fatalf("recovery replay = %#v, %v", recoveryReplay, err)
	}
	duplicate := recoveryRequest
	duplicate.Command.OperationID = "integration-rebase-second-recovery"
	duplicate.SubjectDigest = strings.Repeat("7", 64)
	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), duplicate); !errors.Is(err, application.ErrIntegrationApplicationExists) {
		t.Fatalf("ReserveIntegrationApplication(duplicate recovery) error = %v", err)
	}
}

func TestIntegrationRebaseConflictRecoveryRevalidatesEveryStoredAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, fixture *storedIntegrationFixture, request *application.IntegrationReservationRequest)
	}{
		{name: "missing conflict operation", mutate: func(_ *testing.T, _ *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.Command.RecoveryOperationID = "integration-rebase-conflict-missing"
		}},
		{name: "stored strategy changed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE integration_applications SET strategy = 'merge' WHERE operation_id = ?`, request.Command.RecoveryOperationID)
		}},
		{name: "requested policy changed", mutate: func(_ *testing.T, _ *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.PolicyID = "integration-other"
		}},
		{name: "recovery predates conflict", mutate: func(_ *testing.T, fixture *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.At = fixture.at
		}},
		{name: "initiative unknown", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE initiatives SET state = 'unknown'`)
		}},
		{name: "initiative policy changed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE initiatives SET integration_policy_id = 'integration-other'`)
		}},
		{name: "candidate state changed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE tasks SET state = 'failed' WHERE handle = 'task-component-a'`)
		}},
		{name: "owner not writable", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE tasks SET state = 'paused' WHERE handle = 'task-integration'`)
		}},
		{name: "target preparation missing", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `DELETE FROM task_preparations WHERE task_handle = 'task-integration'`)
		}},
		{name: "candidate preparation missing", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `DELETE FROM task_preparations WHERE task_handle = 'task-component-a'`)
		}},
		{name: "target preparation closed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE task_preparations SET state = 'abandoned' WHERE task_handle = 'task-integration'`)
		}},
		{name: "candidate workspace changed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE task_preparations SET requested_workspace_root = '/approved/workspaces/changed' WHERE task_handle = 'task-component-a'`)
		}},
		{name: "target preparation identity changed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE integration_applications SET target_preparation_operation_id = 'prepare-other-0001' WHERE operation_id = ?`,
				request.Command.RecoveryOperationID)
		}},
		{name: "candidate evidence missing", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `DELETE FROM candidate_evidence WHERE task_handle = 'task-component-a'`)
		}},
		{name: "candidate evidence rejected", mutate: func(t *testing.T, fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE candidate_evidence SET outcome = 'rejected' WHERE task_handle = 'task-component-a'`)
		}},
		{name: "candidate evidence identity changed", mutate: func(t *testing.T, fixture *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			mustExecIntegrationTest(t, fixture, `UPDATE integration_applications SET evidence_digest = ? WHERE operation_id = ?`,
				strings.Repeat("f", 64), request.Command.RecoveryOperationID)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			recovery := storedRebaseRecoveryRequest(t, &fixture)
			test.mutate(t, &fixture, &recovery)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), recovery); err == nil {
				t.Fatal("ReserveIntegrationApplication(altered recovery authority) error = nil")
			}
		})
	}
}

func TestIntegrationRebaseConflictRecoveryAcceptsReadyOwnerAfterRestart(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	recovery := storedRebaseRecoveryRequest(t, &fixture)
	mustExecIntegrationTest(t, &fixture, `UPDATE tasks SET state = CASE handle
		WHEN 'task-integration' THEN 'ready'
		WHEN 'task-component-a' THEN 'delivered'
		ELSE state END`)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), recovery)
	if err != nil || reserved.RecoveryOperationID != recovery.Command.RecoveryOperationID {
		t.Fatalf("ReserveIntegrationApplication(ready recovery owner) = %#v, %v", reserved, err)
	}
}

func TestIntegrationRebaseRecoveryRequiresWriterFreeOwner(t *testing.T) {
	for _, test := range []struct {
		state   domain.TaskState
		allowed bool
	}{
		{state: domain.TaskWorking},
		{state: domain.TaskAwaitingDecision},
		{state: domain.TaskBlocked},
		{state: domain.TaskReady, allowed: true},
	} {
		t.Run(string(test.state), func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			recovery := storedRebaseRecoveryRequest(t, &fixture)
			mustExecIntegrationTest(t, &fixture, `UPDATE tasks SET state = ? WHERE handle = 'task-integration'`, test.state)
			reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), recovery)
			if !test.allowed {
				if !errors.Is(err, application.ErrPrecondition) {
					t.Fatalf("ReserveIntegrationApplication(%s owner) error = %v, want ErrPrecondition", test.state, err)
				}
				return
			}
			if err != nil || reserved.RecoveryOperationID != recovery.Command.RecoveryOperationID {
				t.Fatalf("ReserveIntegrationApplication(%s owner) = %#v, %v", test.state, reserved, err)
			}
			_, err = fixture.store.CommitTaskStart(context.Background(), application.TaskStartMutation{
				TaskHandle: "task-integration", OperationID: "start-integration-during-recovery",
				SubjectDigest: strings.Repeat("7", 64), At: recovery.At.Add(time.Second),
				SchedulingLimits: initiativeTestSchedulingLimits(2),
			})
			if !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("CommitTaskStart(reserved recovery owner) error = %v, want ErrPrecondition", err)
			}
		})
	}
}

func TestIntegrationReservationRejectsAnotherOperationForTheSameCandidate(t *testing.T) {
	for _, outcome := range []string{"reserved", string(application.IntegrationApplied), string(application.IntegrationConflicted)} {
		t.Run(outcome, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			firstRequest := fixture.reservationRequest("integration-first-application", application.IntegrationMerge)
			first, err := fixture.store.ReserveIntegrationApplication(context.Background(), firstRequest)
			if err != nil {
				t.Fatalf("ReserveIntegrationApplication(first) error = %v", err)
			}
			currentHead := firstRequest.Command.ExpectedIntegrationHead
			if outcome != "reserved" {
				adapterResult := application.IntegrationAdapterResult{
					Outcome: application.IntegrationOutcome(outcome), PreviousHead: currentHead,
				}
				if outcome == string(application.IntegrationApplied) {
					adapterResult.ResultingHead = strings.Repeat("d", 40)
					currentHead = adapterResult.ResultingHead
				} else {
					adapterResult.ConflictPaths = []string{"README.md"}
				}
				if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
					Reservation: first, AdapterResult: adapterResult, At: firstRequest.At.Add(time.Second),
				}); err != nil {
					t.Fatalf("CompleteIntegrationApplication(first) error = %v", err)
				}
			}

			secondRequest := fixture.reservationRequest("integration-second-application", application.IntegrationMerge)
			secondRequest.Command.ExpectedIntegrationHead = currentHead
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), secondRequest); !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("ReserveIntegrationApplication(second) error = %v, want ErrPrecondition", err)
			}
			var count int
			if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM integration_applications`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("integration applications after duplicate = %d, %v", count, err)
			}
			if _, err := fixture.store.GetOperation(context.Background(), secondRequest.Command.OperationID); !errors.Is(err, application.ErrNotFound) {
				t.Fatalf("GetOperation(second duplicate) error = %v", err)
			}
		})
	}
}

func TestIntegrationReservationAcceptsReadyOwnerBeforeTerminalLaunch(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	if _, err := fixture.store.db.Exec(`UPDATE tasks SET state = CASE handle
		WHEN 'task-integration' THEN 'ready'
		WHEN 'task-component-a' THEN 'delivered'
		ELSE state END`); err != nil {
		t.Fatal(err)
	}
	request := fixture.reservationRequest("integration-ready-owner", application.IntegrationMerge)

	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatalf("ReserveIntegrationApplication(ready owner) error = %v", err)
	}
	if reserved.Target.TaskHandle != "task-integration" || reserved.Candidate.TaskHandle != "task-component-a" {
		t.Fatalf("ReserveIntegrationApplication(ready owner) = %#v", reserved)
	}
}

func TestIntegrationReservationAcceptsReadyOwnerAfterCandidateRevalidation(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	if _, err := fixture.store.db.Exec(`UPDATE tasks SET state = CASE handle
		WHEN 'task-integration' THEN 'ready'
		WHEN 'task-component-a' THEN 'candidate_complete'
		ELSE state END`); err != nil {
		t.Fatal(err)
	}
	request := fixture.reservationRequest("integration-ready-revalidated-owner", application.IntegrationMerge)

	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatalf("ReserveIntegrationApplication(revalidated candidate) error = %v", err)
	}
	if reserved.Target.TaskHandle != "task-integration" || reserved.Candidate.TaskHandle != "task-component-a" {
		t.Fatalf("ReserveIntegrationApplication(revalidated candidate) = %#v", reserved)
	}
}

func TestIntegrationReservationRejectsMissingAuthorityOrCurrentEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storedIntegrationFixture, *application.IntegrationReservationRequest)
	}{
		{name: "policy differs", mutate: func(_ *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.PolicyID = "integration-other"
		}},
		{name: "caller is not owner", mutate: func(_ *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.Command.IntegrationTaskHandle = "task-component-a"
		}},
		{name: "candidate head differs", mutate: func(_ *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.Command.CandidateHead = strings.Repeat("e", 40)
		}},
		{name: "evidence expired", mutate: func(fixture *storedIntegrationFixture, request *application.IntegrationReservationRequest) {
			request.At = fixture.evidenceExpiresAt
		}},
		{name: "shared worktree", mutate: func(fixture *storedIntegrationFixture, _ *application.IntegrationReservationRequest) {
			if _, err := fixture.store.db.Exec(`UPDATE task_preparations SET requested_workspace_root = ? WHERE task_handle = 'task-integration'`,
				"/approved/workspaces/task-component-a"); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			request := fixture.reservationRequest("integration-refusal-0001", application.IntegrationRebase)
			test.mutate(&fixture, &request)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err == nil {
				t.Fatal("ReserveIntegrationApplication() error = nil")
			}
			var count int
			if err := fixture.store.db.QueryRow(`SELECT COUNT(*) FROM integration_applications`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("integration rows = %d, %v", count, err)
			}
		})
	}
}

func TestIntegrationCompletionRollsBackWhenOperationLedgerFails(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-completion-fault", application.IntegrationMerge)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(`CREATE TRIGGER refuse_integration_operation
		BEFORE UPDATE OF status ON operations
		WHEN OLD.command = 'ApplyIntegrationCandidate' AND NEW.status = 'completed'
		BEGIN SELECT RAISE(ABORT, 'injected integration operation failure'); END`); err != nil {
		t.Fatal(err)
	}
	completion := application.IntegrationCompletion{
		Reservation: reserved,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationApplied, PreviousHead: request.Command.ExpectedIntegrationHead,
			ResultingHead: strings.Repeat("d", 40),
		},
		At: request.At.Add(time.Second),
	}
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), completion); err == nil {
		t.Fatal("CompleteIntegrationApplication(injected fault) error = nil")
	}
	var status string
	if err := fixture.store.db.QueryRow(`SELECT status FROM integration_applications WHERE operation_id = ?`,
		request.Command.OperationID).Scan(&status); err != nil || status != "reserved" {
		t.Fatalf("status after rollback = %q, %v", status, err)
	}
	operation, err := fixture.store.GetOperation(context.Background(), request.Command.OperationID)
	if err != nil || operation.Status != domain.OperationAccepted {
		t.Fatalf("GetOperation(after rollback) = %#v, %v", operation, err)
	}
}

type storedIntegrationFixture struct {
	store             *Store
	databasePath      string
	at                time.Time
	candidateHead     string
	evidenceDigest    string
	evidenceExpiresAt time.Time
}

func newStoredIntegrationFixture(t *testing.T) storedIntegrationFixture {
	return newStoredIntegrationFixtureWithMutation(t, nil)
}

func newStoredIntegrationFixtureWithMutation(
	t *testing.T,
	mutate func(*application.PreparedInitiativeMutation),
) storedIntegrationFixture {
	t.Helper()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	if mutate != nil {
		mutate(&mutation)
	}
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.CommitPreparedInitiative(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	boundAt := mutation.At.Add(time.Minute)
	for handle, state := range map[string]string{
		"task-component-a": "validating",
		"task-integration": "ready",
	} {
		if _, err := store.db.Exec(`UPDATE tasks SET managed_run_id = ?, workspace_lease_id = ?, state = ?, updated_at = ? WHERE handle = ?`,
			"managed-run_"+handle, "workspace-lease_"+handle, state, formatTime(boundAt), handle); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE initiatives SET managed_run_group_id = 'managed-run-group_integration', state = 'active', updated_at = ? WHERE handle = ?`,
		formatTime(boundAt), mutation.Initiative.Handle); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.GetTask(context.Background(), "task-component-a")
	if err != nil {
		t.Fatal(err)
	}
	candidateHead := strings.Repeat("b", 40)
	evidence := candidateEvidence(t, candidate, candidateHead)
	judgedAt := candidate.UpdatedAt.Add(5 * time.Minute)
	if _, _, err := store.CommitCandidateEvidence(context.Background(), candidate.Handle, evidence,
		[]string{"unit"}, []string{"ci/unit"}, judgedAt, candidateEvidencePublications(t, candidate, evidence)); err != nil {
		t.Fatal(err)
	}
	bundle := evidence.Bundle()
	return storedIntegrationFixture{
		store: store, databasePath: databasePath, at: judgedAt.Add(time.Minute), candidateHead: candidateHead,
		evidenceDigest: evidence.Digest(), evidenceExpiresAt: bundle.ExpiresAt,
	}
}

func (fixture storedIntegrationFixture) reservationRequest(
	operationID string,
	strategy application.IntegrationStrategy,
) application.IntegrationReservationRequest {
	return application.IntegrationReservationRequest{
		Command: application.ApplyIntegrationCandidateCommand{
			OperationID: operationID, InitiativeHandle: "initiative-prepare-0001",
			IntegrationTaskHandle: "task-integration", CandidateTaskHandle: "task-component-a",
			CandidateHead: fixture.candidateHead, ExpectedIntegrationHead: strings.Repeat("c", 40),
		},
		PolicyID: "integration-default", Strategy: strategy,
		SubjectDigest: strings.Repeat("9", 64), At: fixture.at,
	}
}

func storedRebaseRecoveryRequest(
	t *testing.T,
	fixture *storedIntegrationFixture,
) application.IntegrationReservationRequest {
	t.Helper()
	initialRequest := fixture.reservationRequest("integration-rebase-authority-conflict", application.IntegrationRebase)
	initial, err := fixture.store.ReserveIntegrationApplication(context.Background(), initialRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: initial,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationConflicted, PreviousHead: initial.Target.ExpectedHead,
			ConflictPaths: []string{"fixture.txt"},
		},
		At: initialRequest.At.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	recovery := fixture.reservationRequest("integration-rebase-authority-recovery", application.IntegrationRebase)
	recovery.Command.RecoveryOperationID = initial.OperationID
	recovery.SubjectDigest = strings.Repeat("8", 64)
	recovery.At = fixture.evidenceExpiresAt.Add(time.Hour)
	return recovery
}

func mustExecIntegrationTest(t *testing.T, fixture *storedIntegrationFixture, statement string, arguments ...any) {
	t.Helper()
	if _, err := fixture.store.db.Exec(statement, arguments...); err != nil {
		t.Fatal(err)
	}
}

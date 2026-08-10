package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestMutationReplayConflictIsDurablyAuditedWithoutChangingOriginal(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutations := sqliteMutations(t, store, &sequenceIDs{ids: []string{"task-audit"}},
		time.Date(2026, time.August, 9, 21, 0, 0, 0, time.UTC))
	command := sqlitePrepareCommand()
	if _, err := mutations.PrepareTask(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	altered := command
	altered.AcceptanceCriteria = []string{"This altered subject must be denied."}
	if _, err := mutations.PrepareTask(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("PrepareTask(altered) error = %v, want ErrConflict", err)
	}
	var count int
	if err := store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM operation_replay_conflicts WHERE operation_id = ?`, command.OperationID,
	).Scan(&count); err != nil {
		t.Fatalf("read replay-conflict audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("durable replay-conflict audit count = %d, want 1", count)
	}
}

func TestManagedRunLifecycleStoreRejectsUnsafeDirectMutations(t *testing.T) {
	t.Run("activation joins", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*application.ManagedRunActivationMutation)
			want   error
		}{
			{name: "service", mutate: func(mutation *application.ManagedRunActivationMutation) { mutation.ServiceInstanceID = "service-other" }, want: application.ErrPrecondition},
			{name: "nonce", mutate: func(mutation *application.ManagedRunActivationMutation) {
				mutation.RegistrationNonce = "registration-nonce-wrong"
			}, want: application.ErrPrecondition},
			{name: "expired", mutate: func(mutation *application.ManagedRunActivationMutation) { mutation.At = mutation.At.Add(2 * time.Hour) }, want: application.ErrPrecondition},
			{name: "non UTC", mutate: func(mutation *application.ManagedRunActivationMutation) {
				mutation.At = mutation.At.In(time.FixedZone("offset", 3600))
			}, want: application.ErrPrecondition},
			{name: "lease invariant", mutate: func(mutation *application.ManagedRunActivationMutation) { mutation.Binding.WorkspaceLeaseID = "" }, want: application.ErrInvalidInput},
		} {
			t.Run(test.name, func(t *testing.T) {
				store, prepared, mutation := lifecycleStore(t, true)
				test.mutate(&mutation)
				if _, err := store.CommitManagedRunActivation(context.Background(), mutation); !errors.Is(err, test.want) {
					t.Fatalf("CommitManagedRunActivation() error = %v, want %v", err, test.want)
				}
				task, err := store.GetTask(context.Background(), prepared.Task.Handle)
				if err != nil || task.State != domain.TaskPrepared {
					t.Fatalf("rejected activation task = %#v, %v", task, err)
				}
			})
		}
	})

	t.Run("workspace-less DevCrew preparation", func(t *testing.T) {
		store, _, mutation := lifecycleStore(t, false)
		mutation.Binding.WorkspaceLeaseID = ""
		if _, err := store.CommitManagedRunActivation(context.Background(), mutation); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("CommitManagedRunActivation() error = %v, want ErrPrecondition", err)
		}
	})

	t.Run("invalid direct binding", func(t *testing.T) {
		store, _, mutation := lifecycleStore(t, true)
		mutation.Binding.ManagedRunID = ""
		if _, err := store.CommitManagedRunActivation(context.Background(), mutation); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("CommitManagedRunActivation(invalid binding) error = %v", err)
		}
	})

	t.Run("missing and already active", func(t *testing.T) {
		store, _, mutation := lifecycleStore(t, true)
		missing := mutation
		missing.ExternalRunRef = "task-missing"
		missing.OperationID = "operation-activate-missing"
		if _, err := store.CommitManagedRunActivation(context.Background(), missing); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("missing activation error = %v", err)
		}
		if _, err := store.CommitManagedRunActivation(context.Background(), mutation); err != nil {
			t.Fatal(err)
		}
		second := mutation
		second.OperationID = "operation-activate-second"
		second.SubjectDigest = strings.Repeat("b", 64)
		if _, err := store.CommitManagedRunActivation(context.Background(), second); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("second activation error = %v, want ErrPrecondition", err)
		}
	})

	t.Run("abandon joins", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*application.ManagedRunAbandonMutation)
			want   error
		}{
			{name: "missing", mutate: func(mutation *application.ManagedRunAbandonMutation) { mutation.ExternalRunRef = "task-missing" }, want: application.ErrNotFound},
			{name: "service", mutate: func(mutation *application.ManagedRunAbandonMutation) { mutation.ServiceInstanceID = "service-other" }, want: application.ErrPrecondition},
			{name: "nonce", mutate: func(mutation *application.ManagedRunAbandonMutation) {
				mutation.RegistrationNonce = "registration-nonce-wrong"
			}, want: application.ErrPrecondition},
			{name: "non UTC", mutate: func(mutation *application.ManagedRunAbandonMutation) {
				mutation.At = mutation.At.In(time.FixedZone("offset", 3600))
			}, want: application.ErrPrecondition},
			{name: "disposition", mutate: func(mutation *application.ManagedRunAbandonMutation) { mutation.Disposition = "invented" }, want: application.ErrInvalidInput},
		} {
			t.Run(test.name, func(t *testing.T) {
				store, _, activation := lifecycleStore(t, true)
				mutation := abandonMutation(activation)
				test.mutate(&mutation)
				if _, err := store.CommitManagedRunAbandon(context.Background(), mutation); !errors.Is(err, test.want) {
					t.Fatalf("CommitManagedRunAbandon() error = %v, want %v", err, test.want)
				}
			})
		}
	})

	t.Run("already closed or active", func(t *testing.T) {
		store, _, activation := lifecycleStore(t, true)
		abandon := abandonMutation(activation)
		if _, err := store.CommitManagedRunAbandon(context.Background(), abandon); err != nil {
			t.Fatal(err)
		}
		second := abandon
		second.OperationID = "operation-abandon-second"
		second.SubjectDigest = strings.Repeat("c", 64)
		if _, err := store.CommitManagedRunAbandon(context.Background(), second); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("second abandon error = %v", err)
		}

		activeStore, _, active := lifecycleStore(t, true)
		if _, err := activeStore.CommitManagedRunActivation(context.Background(), active); err != nil {
			t.Fatal(err)
		}
		if _, err := activeStore.CommitManagedRunAbandon(context.Background(), abandonMutation(active)); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("abandon active error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("closed database", func(t *testing.T) {
		store, prepared, activation := lifecycleStore(t, true)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitManagedRunActivation(context.Background(), activation); err == nil {
			t.Fatal("CommitManagedRunActivation(closed) error = nil")
		}
		if _, err := store.CommitManagedRunAbandon(context.Background(), abandonMutation(activation)); err == nil {
			t.Fatal("CommitManagedRunAbandon(closed) error = nil")
		}
		if _, err := store.GetManagedRunPreparation(context.Background(), prepared.Task.Handle); err == nil {
			t.Fatal("GetManagedRunPreparation(closed) error = nil")
		}
	})
}

func TestManagedRunLifecycleStoreInternalFailureBoundaries(t *testing.T) {
	t.Run("exhausted activation and abandonment", func(t *testing.T) {
		for _, abandon := range []bool{false, true} {
			store, _, activation := lifecycleStore(t, true)
			exhausted := storeOperation("operation-exhausted-lifecycle", int64(^uint64(0)>>1))
			exhausted.CreatedAt, exhausted.UpdatedAt = activation.At, activation.At
			if err := store.RecordOperation(context.Background(), exhausted); err != nil {
				t.Fatal(err)
			}
			if abandon {
				if _, err := store.CommitManagedRunAbandon(context.Background(), abandonMutation(activation)); err == nil {
					t.Fatal("CommitManagedRunAbandon(exhausted) error = nil")
				}
			} else if _, err := store.CommitManagedRunActivation(context.Background(), activation); err == nil {
				t.Fatal("CommitManagedRunActivation(exhausted) error = nil")
			}
		}
	})

	t.Run("preparation closure update", func(t *testing.T) {
		store, prepared, activation := lifecycleStore(t, true)
		task := prepared.Task
		invalid := *prepared.Preparation
		if err := updateManagedRunPreparation(context.Background(), store.db, task, invalid); err == nil {
			t.Fatal("updateManagedRunPreparation(open) error = nil")
		}
		closedAt := activation.At
		closed := invalid
		closed.State = application.PreparationAbandoned
		closed.AbandonReason = application.AbandonReasonOwnerCancelled
		closed.Disposition = application.AbandonDispositionPreserve
		closed.ClosedAt = &closedAt
		if err := updateManagedRunPreparation(context.Background(), store.db, task, closed); err != nil {
			t.Fatal(err)
		}
		if err := updateManagedRunPreparation(context.Background(), store.db, task, closed); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("updateManagedRunPreparation(repeat) error = %v", err)
		}
	})

	t.Run("corrupt closure read", func(t *testing.T) {
		store, prepared, _ := lifecycleStore(t, true)
		if _, err := store.db.Exec(`UPDATE task_preparations SET state = 'abandoned',
			abandon_reason = 'owner_cancelled', disposition = 'preserve', closed_at = 'not-a-time'`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetManagedRunPreparation(context.Background(), prepared.Task.Handle); err == nil {
			t.Fatal("GetManagedRunPreparation(corrupt closure) error = nil")
		}
	})

	t.Run("corrupt preparation state read", func(t *testing.T) {
		store, prepared, _ := lifecycleStore(t, true)
		if _, err := store.db.Exec(`UPDATE task_preparations SET state = 'invented'`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetManagedRunPreparation(context.Background(), prepared.Task.Handle); err == nil {
			t.Fatal("GetManagedRunPreparation(corrupt state) error = nil")
		}
	})

	t.Run("invalid direct abandon reason", func(t *testing.T) {
		store, _, activation := lifecycleStore(t, true)
		abandon := abandonMutation(activation)
		abandon.Reason = "invented"
		if _, err := store.CommitManagedRunAbandon(context.Background(), abandon); err == nil {
			t.Fatal("CommitManagedRunAbandon(invalid reason) error = nil")
		}
	})

	t.Run("non-conflict transaction helper", func(t *testing.T) {
		store, _, _ := lifecycleStore(t, true)
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = transaction.Rollback() }()
		cause := errors.New("not a replay conflict")
		if got := commitReplayConflict(transaction, cause); !errors.Is(got, cause) {
			t.Fatalf("commitReplayConflict() = %v", got)
		}
	})

	t.Run("failed conflict-audit commit", func(t *testing.T) {
		store, _, _ := lifecycleStore(t, true)
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := commitReplayConflict(transaction, application.ErrConflict); err == nil {
			t.Fatal("commitReplayConflict(closed transaction) error = nil")
		}
	})

	t.Run("incomplete and corrupt transactional replay", func(t *testing.T) {
		store, _, activation := lifecycleStore(t, true)
		incomplete := storeOperation(activation.OperationID, 2)
		incomplete.Command = commandActivateManagedRun
		incomplete.SubjectDigest = activation.SubjectDigest
		incomplete.ResultRef = ""
		incomplete.CreatedAt, incomplete.UpdatedAt = activation.At, activation.At
		if err := store.RecordOperation(context.Background(), incomplete); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitManagedRunActivation(context.Background(), activation); err == nil {
			t.Fatal("CommitManagedRunActivation(incomplete replay) error = nil")
		}

		corruptStore, _, corruptActivation := lifecycleStore(t, true)
		const insert = `INSERT INTO operations (
			id, schema_version, command, subject_digest, status, error_code,
			result_ref, state_version, created_at, updated_at
		) VALUES (?, 1, ?, ?, 'corrupt', '', '', 2, ?, ?)`
		if _, err := corruptStore.db.ExecContext(context.Background(), insert,
			corruptActivation.OperationID, commandActivateManagedRun, corruptActivation.SubjectDigest,
			formatTime(corruptActivation.At), formatTime(corruptActivation.At)); err != nil {
			t.Fatal(err)
		}
		if _, err := corruptStore.CommitManagedRunActivation(context.Background(), corruptActivation); err == nil {
			t.Fatal("CommitManagedRunActivation(corrupt replay) error = nil")
		}
	})

	t.Run("state update requires exact task", func(t *testing.T) {
		store, _, activation := lifecycleStore(t, true)
		transaction, err := store.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = transaction.Rollback() }()
		missing := storeTask("task-not-stored", 2)
		missing.CreatedAt, missing.UpdatedAt = activation.At, activation.At
		if err := updateTaskState(context.Background(), transaction, missing); err == nil {
			t.Fatal("updateTaskState(missing) error = nil")
		}
	})
}

func lifecycleStore(t *testing.T, withWorkspace bool) (*Store, application.MutationResult, application.ManagedRunActivationMutation) {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 9, 21, 0, 0, 0, time.UTC)
	workspace := ""
	if withWorkspace {
		workspace = "/approved/workspaces/task-lifecycle"
	}
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: configuredCatalog{},
		Workspaces:         configuredWorkspacePreparer{root: workspace},
		RuntimeAttachments: configuredRuntimeAttachments{},
		TaskIDs:            func(string) (string, error) { return "task-lifecycle", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce-lifecycle", nil },
		PreparationTTL:     time.Hour, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	command := sqlitePrepareCommand()
	command.OperationID = "operation-prepare-lifecycle"
	prepared, err := mutations.PrepareTask(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	activation := application.ManagedRunActivationMutation{
		ServiceInstanceID: command.ServiceInstanceID, ExternalRunRef: prepared.Task.Handle,
		RegistrationNonce: prepared.Preparation.RegistrationNonce,
		Binding:           domain.TaskBinding{ManagedRunID: "managed-run-lifecycle", WorkspaceLeaseID: "workspace-lease-lifecycle"},
		OperationID:       "operation-activate-lifecycle", SubjectDigest: strings.Repeat("a", 64), At: now.Add(time.Minute),
	}
	return store, prepared, activation
}

func abandonMutation(activation application.ManagedRunActivationMutation) application.ManagedRunAbandonMutation {
	return application.ManagedRunAbandonMutation{
		ServiceInstanceID: activation.ServiceInstanceID, ExternalRunRef: activation.ExternalRunRef,
		RegistrationNonce: activation.RegistrationNonce, Reason: application.AbandonReasonOwnerCancelled,
		Disposition: application.AbandonDispositionPreserve,
		OperationID: "operation-abandon-lifecycle", SubjectDigest: strings.Repeat("d", 64), At: activation.At,
	}
}

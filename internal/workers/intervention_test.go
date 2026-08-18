package workers_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

// Both shipped adapters are driven through one table. The adapter contract is
// only frozen if a second, materially different worker family answers the same
// questions the first one does, and reaching a running worker — steering it,
// asking it to settle, asking it to stop — is the half that launch and activity
// classification never exercised.
func interventionAdapters(t *testing.T) map[string]application.WorkerHarnessAdapter {
	t.Helper()

	codexProfile := availableCodexProfile(codexFixtureExecutable(t), "codex-reviewed")
	// The attachment keys are what a launch or resume descriptor binds; the
	// shared fixture allows only the socket path.
	codexProfile.EnvironmentKeys = append(
		codexProfile.EnvironmentKeys,
		"COMIS_EXECUTION_ATTACHMENT_TARGET_NAME", "COMIS_EXECUTION_ATTACHMENT_IDENTITY",
	)
	codexCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{codexProfile})
	if err != nil {
		t.Fatal(err)
	}
	codex, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: codexCatalog, ProfileID: codexProfile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	claudeProfile := availableClaudeProfile(codexFixtureExecutable(t), "claude-reviewed")
	claudeCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{claudeProfile})
	if err != nil {
		t.Fatal(err)
	}
	claude, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: claudeCatalog, ProfileID: claudeProfile.ID,
		ExpectedVersion: "2.0.30 (Claude Code)", ConfigDirectory: claudeConfigDirectory(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	return map[string]application.WorkerHarnessAdapter{
		"codex": codex, "claude": claude,
	}
}

func interventionProfileID(name string) string {
	if name == "codex" {
		return "codex-reviewed"
	}
	return "claude-reviewed"
}

// An instruction is delivered only into an affirmatively empty composer. A
// pending line may be a human's half-typed message or a worker's own prompt,
// and an unknown state may be either — injecting into one types the operator's
// instruction into the middle of somebody else's sentence. Neither defers to a
// guess: both refuse and leave the decision to the service.
func TestAdapters_SendInputRefusesEveryComposerThatIsNotAffirmativelyEmpty(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			for _, composer := range []application.ComposerState{
				application.ComposerPending, application.ComposerUnknown, application.ComposerState("typing"), "",
			} {
				_, err := adapter.SendInput(context.Background(), application.WorkerInputRequest{
					ProfileID:   interventionProfileID(name),
					TaskHandle:  "task-0001",
					Instruction: "Prefer the smaller change.",
					Composer:    composer,
				})
				if !errors.Is(err, application.ErrComposerNotEmpty) {
					t.Fatalf("SendInput(composer=%q) error = %v, want ErrComposerNotEmpty", composer, err)
				}
			}
		})
	}
}

// The instruction is typed once. Only the submission keystroke may be retried:
// a resent instruction is a second instruction, and the worker has no way to
// know the operator meant one.
func TestAdapters_SendInputTypesTheInstructionOnceAndRetriesOnlySubmission(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			plan, err := adapter.SendInput(context.Background(), application.WorkerInputRequest{
				ProfileID:   interventionProfileID(name),
				TaskHandle:  "task-0001",
				Instruction: "Prefer the smaller change.",
				Composer:    application.ComposerEmpty,
			})
			if err != nil {
				t.Fatalf("SendInput() error = %v", err)
			}
			if plan.Kind != application.WorkerInputInstruction {
				t.Errorf("plan kind = %q, want %q", plan.Kind, application.WorkerInputInstruction)
			}
			if plan.Text != "Prefer the smaller change." {
				t.Errorf("plan text = %q, want the instruction verbatim", plan.Text)
			}
			if plan.TextAttempts != 1 {
				t.Errorf("plan text attempts = %d, want exactly one", plan.TextAttempts)
			}
			if plan.SubmitAttempts < 2 {
				t.Errorf("plan submit attempts = %d, want the submission to be retryable", plan.SubmitAttempts)
			}
			if plan.SubmitKey == "" {
				t.Error("plan states no submission keystroke")
			}
			if plan.RequiredComposer != application.ComposerEmpty {
				t.Errorf("plan required composer = %q, want empty", plan.RequiredComposer)
			}
			// An unconfirmed submission is reconciled, never quietly resent:
			// the service cannot tell a lost keystroke from a delivered one.
			if !plan.ReconcileWhenUnconfirmed {
				t.Error("plan must require reconciliation when submission is unconfirmed")
			}
		})
	}
}

// A slash or skill invocation makes the harness open its own picker, and
// submitting into that picker selects an entry instead of sending the line.
// Every adapter must settle longer for one, and the pause is harness-specific
// because the pickers are.
func TestAdapters_SendInputSettlesLongerForASlashInvocation(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			plain, err := adapter.SendInput(context.Background(), application.WorkerInputRequest{
				ProfileID: interventionProfileID(name), TaskHandle: "task-0001",
				Instruction: "Prefer the smaller change.", Composer: application.ComposerEmpty,
			})
			if err != nil {
				t.Fatal(err)
			}
			slash, err := adapter.SendInput(context.Background(), application.WorkerInputRequest{
				ProfileID: interventionProfileID(name), TaskHandle: "task-0001",
				Instruction: "/compact", Composer: application.ComposerEmpty,
			})
			if err != nil {
				t.Fatal(err)
			}
			if slash.SettlePause <= plain.SettlePause {
				t.Errorf("slash settle pause = %v, want longer than %v", slash.SettlePause, plain.SettlePause)
			}
			if plain.SettlePause <= 0 {
				t.Errorf("settle pause = %v, want a positive pause before submitting", plain.SettlePause)
			}
		})
	}
}

// An instruction carrying its own newline would submit itself mid-text, and a
// control sequence would reach the terminal rather than the worker. Both are
// refused rather than sanitized: an instruction the operator did not write is
// not the instruction they asked to send.
func TestAdapters_SendInputRefusesInstructionsThatCarryTheirOwnSubmission(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			for _, instruction := range []string{
				"", "  ", "one\ntwo", "one\rtwo", "one\x00two", "one\x1b[Atwo",
				strings.Repeat("x", 8193),
			} {
				_, err := adapter.SendInput(context.Background(), application.WorkerInputRequest{
					ProfileID: interventionProfileID(name), TaskHandle: "task-0001",
					Instruction: instruction, Composer: application.ComposerEmpty,
				})
				if !errors.Is(err, application.ErrWorkerInputUnsafe) {
					t.Fatalf("SendInput(%q) error = %v, want ErrWorkerInputUnsafe", instruction, err)
				}
			}
		})
	}
}

// Pause asks the worker to reach a safe boundary; stop asks it to end. Neither
// carries operator text, and neither may be delivered as an instruction — a
// worker that read "stop" as a message would answer it instead of obeying it.
func TestAdapters_PauseAndStopCarryNoOperatorTextAndAreDistinctFromInput(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			pause, err := adapter.RequestPause(context.Background(), application.WorkerControlRequest{
				ProfileID: interventionProfileID(name), TaskHandle: "task-0001",
			})
			if err != nil {
				t.Fatalf("RequestPause() error = %v", err)
			}
			stop, err := adapter.RequestStop(context.Background(), application.WorkerControlRequest{
				ProfileID: interventionProfileID(name), TaskHandle: "task-0001",
			})
			if err != nil {
				t.Fatalf("RequestStop() error = %v", err)
			}
			if pause.Kind != application.WorkerInputPause || stop.Kind != application.WorkerInputStop {
				t.Fatalf("kinds = %q/%q, want pause/stop", pause.Kind, stop.Kind)
			}
			for label, plan := range map[string]application.WorkerInputPlan{"pause": pause, "stop": stop} {
				if plan.Text != "" {
					t.Errorf("%s carries operator text %q", label, plan.Text)
				}
				if plan.ControlKey == "" {
					t.Errorf("%s states no control keystroke", label)
				}
				if !plan.ReconcileWhenUnconfirmed {
					t.Errorf("%s must be reconciled when unconfirmed", label)
				}
			}
			if pause.ControlKey == stop.ControlKey {
				t.Errorf("pause and stop share the keystroke %q", pause.ControlKey)
			}
		})
	}
}

// The adapter refuses a profile it does not own. Resolution is exact: a
// descriptor built from another family's profile would launch the wrong binary
// with this family's arguments.
func TestAdapters_InterventionRefusesAProfileTheAdapterDoesNotOwn(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := adapter.SendInput(context.Background(), application.WorkerInputRequest{
				ProfileID: "someone-elses-profile", TaskHandle: "task-0001",
				Instruction: "Prefer the smaller change.", Composer: application.ComposerEmpty,
			}); err == nil {
				t.Error("SendInput(foreign profile) error = nil")
			}
			if _, err := adapter.RequestPause(context.Background(), application.WorkerControlRequest{
				ProfileID: "someone-elses-profile", TaskHandle: "task-0001",
			}); err == nil {
				t.Error("RequestPause(foreign profile) error = nil")
			}
			if _, err := adapter.RequestStop(context.Background(), application.WorkerControlRequest{
				ProfileID: "someone-elses-profile", TaskHandle: "task-0001",
			}); err == nil {
				t.Error("RequestStop(foreign profile) error = nil")
			}
		})
	}
}

// resumeLaunchRequest is the launch binding a resume is built from. Task, run
// and lease identity are deliberately recognizable strings so a test can prove
// none of them reached argv.
func resumeLaunchRequest(t *testing.T, name string) application.WorkerLaunchRequest {
	t.Helper()
	attachmentTarget := "attachment-0123456789abcdef0123456789abcdef.sock"
	return application.WorkerLaunchRequest{
		ProfileID: interventionProfileID(name), Shape: domain.ShapeShip,
		WorkingDirectory: canonicalWorkerTempDir(t),
		TaskHandle:       "task-secret-0001", ManagedRunID: "managed-run-secret-0001",
		WorkspaceLeaseID: "workspace-lease-secret-0001", BriefRevision: 3,
		BriefRevisionHash: strings.Repeat("a", 64),
		Attachment: application.RuntimeSocketAttachment{
			ExecutionAttachmentID: "execution-attachment-secret-0001",
			AttachmentTargetName:  attachmentTarget,
			MountSocketPath:       "/run/comis/attachments/" + attachmentTarget,
			RelayIdentity:         strings.Repeat("ab", 32),
		},
	}
}

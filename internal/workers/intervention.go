package workers

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// Harness-specific delivery facts. Each family opens its own picker on a slash
// or skill invocation and submitting into that picker selects an entry instead
// of sending the line, so the settle pause is per-family and measured against
// that picker, not guessed from a shared default.
const (
	codexSubmitKey       = "Enter"
	codexSettlePause     = 250 * time.Millisecond
	codexPickerPause     = 1200 * time.Millisecond
	codexPauseControlKey = "Escape"
	codexStopControlKey  = "C-c"

	claudeSubmitKey       = "Enter"
	claudeSettlePause     = 300 * time.Millisecond
	claudePickerPause     = 1500 * time.Millisecond
	claudePauseControlKey = "Escape"
	claudeStopControlKey  = "C-c"

	// The submission keystroke is retried because repeating it against an
	// already-submitted line is inert. The instruction itself never is.
	workerSubmitAttempts = 3
)

// errArgumentsMissing guards a descriptor with no bootstrap to replace.
var errArgumentsMissing = errors.New("build resume descriptor: reviewed launch argv is empty")

// invocationPrefixes open a harness picker rather than composing a line.
var invocationPrefixes = []string{"/", "@", "#"}

// settlePause returns the pause to apply after typing and before submitting.
// An invocation needs the longer one: its picker is still resolving, and a
// submission that arrives first chooses whatever the picker had highlighted.
func settlePause(instruction string, composed, picker time.Duration) time.Duration {
	for _, prefix := range invocationPrefixes {
		if strings.HasPrefix(instruction, prefix) {
			return picker
		}
	}
	return composed
}

// SendInput states how to deliver one instruction to the Codex worker running a
// task. It never types: the returned plan is performed by the service.
func (adapter *CodexAdapter) SendInput(
	ctx context.Context,
	request application.WorkerInputRequest,
) (application.WorkerInputPlan, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.WorkerInputPlan{}, err
	}
	return application.PlanWorkerInstruction(
		request, codexSubmitKey,
		settlePause(request.Instruction, codexSettlePause, codexPickerPause),
		workerSubmitAttempts,
	)
}

// RequestPause asks the Codex worker to reach a safe boundary and stop. It
// carries no instruction: a pause that could also say something would be a
// steer wearing a different name.
func (adapter *CodexAdapter) RequestPause(
	ctx context.Context,
	request application.WorkerControlRequest,
) (application.WorkerInputPlan, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.WorkerInputPlan{}, err
	}
	return application.PlanWorkerControl(application.WorkerInputPause, codexPauseControlKey, codexSettlePause)
}

// RequestStop asks the Codex worker to end the turn it is running.
func (adapter *CodexAdapter) RequestStop(
	ctx context.Context,
	request application.WorkerControlRequest,
) (application.WorkerInputPlan, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.WorkerInputPlan{}, err
	}
	return application.PlanWorkerControl(application.WorkerInputStop, codexStopControlKey, codexSettlePause)
}

func (adapter *CodexAdapter) readyFor(ctx context.Context, profileID string) error {
	return adapterReadyFor(ctx, adapter == nil || adapter.profiles == nil, adapter.profileIdentity(), profileID)
}

func (adapter *CodexAdapter) profileIdentity() string {
	if adapter == nil {
		return ""
	}
	return adapter.profileID
}

// SendInput states how to deliver one instruction to the Claude Code worker
// running a task.
func (adapter *ClaudeAdapter) SendInput(
	ctx context.Context,
	request application.WorkerInputRequest,
) (application.WorkerInputPlan, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.WorkerInputPlan{}, err
	}
	return application.PlanWorkerInstruction(
		request, claudeSubmitKey,
		settlePause(request.Instruction, claudeSettlePause, claudePickerPause),
		workerSubmitAttempts,
	)
}

// RequestPause asks the Claude Code worker to reach a safe boundary and stop.
func (adapter *ClaudeAdapter) RequestPause(
	ctx context.Context,
	request application.WorkerControlRequest,
) (application.WorkerInputPlan, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.WorkerInputPlan{}, err
	}
	return application.PlanWorkerControl(application.WorkerInputPause, claudePauseControlKey, claudeSettlePause)
}

// RequestStop asks the Claude Code worker to end the turn it is running.
func (adapter *ClaudeAdapter) RequestStop(
	ctx context.Context,
	request application.WorkerControlRequest,
) (application.WorkerInputPlan, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.WorkerInputPlan{}, err
	}
	return application.PlanWorkerControl(application.WorkerInputStop, claudeStopControlKey, claudeSettlePause)
}

func (adapter *ClaudeAdapter) readyFor(ctx context.Context, profileID string) error {
	return adapterReadyFor(ctx, adapter == nil || adapter.profiles == nil, adapter.profileIdentity(), profileID)
}

func (adapter *ClaudeAdapter) profileIdentity() string {
	if adapter == nil {
		return ""
	}
	return adapter.profileID
}

// adapterReadyFor refuses a request aimed at a profile this adapter does not
// own. Resolution stays exact: a plan built from another family's profile would
// send this family's keystrokes to a harness that means something else by them.
func adapterReadyFor(ctx context.Context, unavailable bool, owned, requested string) error {
	if ctx == nil {
		return errors.New("plan worker interaction: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if unavailable {
		return errors.New("plan worker interaction: adapter is unavailable")
	}
	if requested != owned {
		return ErrProfileUnknown
	}
	return nil
}

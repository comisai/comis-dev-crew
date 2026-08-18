package application

import (
	"errors"
	"time"
)

// The adapter does not type into a terminal. It states an exact, reviewed
// delivery plan and the service performs it through the scoped host control
// operation, so terminal authority stays in one place and every harness
// family's rules stay declarative and testable.

// ComposerState is the closed observed state of a worker's input composer.
//
// Only an affirmatively empty composer may receive an instruction. A pending
// line may be a human's half-typed message or the worker's own prompt, and an
// unknown state may be either; delivering into one types the operator's
// instruction into the middle of somebody else's sentence. Absence of evidence
// is `ComposerUnknown` and never `ComposerEmpty`.
type ComposerState string

const (
	ComposerEmpty   ComposerState = "empty"
	ComposerPending ComposerState = "pending"
	ComposerUnknown ComposerState = "unknown"
)

func (state ComposerState) valid() bool {
	switch state {
	case ComposerEmpty, ComposerPending, ComposerUnknown:
		return true
	default:
		return false
	}
}

// WorkerInputKind separates the three things a service may deliver to a running
// worker. They are not interchangeable: a worker that received "stop" as an
// instruction would answer it rather than obey it.
type WorkerInputKind string

const (
	WorkerInputInstruction WorkerInputKind = "instruction"
	WorkerInputPause       WorkerInputKind = "pause"
	WorkerInputStop        WorkerInputKind = "stop"
)

// MaximumWorkerInstructionBytes bounds one steer before it is planned, so an
// oversized instruction is refused rather than truncated into a different one.
const MaximumWorkerInstructionBytes = 8192

var (
	// ErrComposerNotEmpty defers delivery. It is not a failure of the
	// instruction: the service reobserves and asks again.
	ErrComposerNotEmpty = errors.New("worker composer is not affirmatively empty")
	// ErrWorkerInputUnsafe rejects an instruction that would submit itself,
	// reach the terminal instead of the worker, or exceed its bound.
	ErrWorkerInputUnsafe = errors.New("worker instruction is unsafe to deliver")
)

// WorkerInputRequest asks one adapter how to deliver one instruction to the
// worker running a task. The composer state is evidence the service observed;
// the adapter never infers it.
type WorkerInputRequest struct {
	ProfileID   string
	TaskHandle  string
	Instruction string
	Composer    ComposerState
}

// WorkerControlRequest asks one adapter how to ask a worker to settle or stop.
// It carries no operator text on purpose.
type WorkerControlRequest struct {
	ProfileID  string
	TaskHandle string
}

// WorkerInputPlan is the reviewed one-shot delivery contract for one
// interaction with a running worker.
//
// The separation of TextAttempts from SubmitAttempts is the contract, not a
// tuning knob. The instruction is typed once because a resent instruction is a
// second instruction the worker cannot distinguish from the first; only the
// submission keystroke may be retried, because repeating it against an already
// submitted line is inert while repeating the text is not.
type WorkerInputPlan struct {
	Kind WorkerInputKind
	// Text is the exact operator instruction, empty for pause and stop.
	Text string
	// SubmitKey submits Text. Empty for pause and stop, which carry no text.
	SubmitKey string
	// ControlKey is the harness-specific keystroke for pause or stop, empty
	// for an instruction.
	ControlKey string
	// SettlePause is how long to wait after typing before submitting. A slash
	// or skill invocation opens the harness's own picker, and submitting into
	// that picker selects an entry instead of sending the line.
	SettlePause time.Duration
	// TextAttempts is always exactly one.
	TextAttempts int
	// SubmitAttempts bounds retries of the submission keystroke alone.
	SubmitAttempts int
	// RequiredComposer is the composer state this plan was built for. The
	// service reconfirms it immediately before delivering.
	RequiredComposer ComposerState
	// ReconcileWhenUnconfirmed is always true: an unconfirmed submission is a
	// loud failure the service reconciles, never a silent resend. The service
	// cannot tell a lost keystroke from a delivered one whose acknowledgement
	// was lost, and guessing wrong duplicates the operator's instruction.
	ReconcileWhenUnconfirmed bool
}

// ValidateWorkerInstruction rejects an instruction that would submit itself,
// carry a terminal control sequence, or exceed its bound. It refuses rather
// than sanitizing: an instruction the operator did not write is not the
// instruction they asked to send.
func ValidateWorkerInstruction(instruction string) error {
	if len([]byte(instruction)) > MaximumWorkerInstructionBytes {
		return ErrWorkerInputUnsafe
	}
	if len(instruction) == 0 {
		return ErrWorkerInputUnsafe
	}
	var visible bool
	for _, character := range instruction {
		if character == '\n' || character == '\r' || character == 0x00 || character == 0x1b {
			return ErrWorkerInputUnsafe
		}
		// Every control character is refused, tab included: in a TUI composer
		// tab is a completion or focus key, so it acts on the composer rather
		// than becoming part of the line.
		if character < 0x20 || character == 0x7f {
			return ErrWorkerInputUnsafe
		}
		if character != ' ' {
			visible = true
		}
	}
	if !visible {
		return ErrWorkerInputUnsafe
	}
	return nil
}

// PlanWorkerInstruction builds the shared half of every family's instruction
// plan. Harness-specific keystrokes and settle pauses are supplied by the
// adapter; the invariants that came from the failure history are not.
func PlanWorkerInstruction(
	request WorkerInputRequest,
	submitKey string,
	settlePause time.Duration,
	submitAttempts int,
) (WorkerInputPlan, error) {
	if !request.Composer.valid() || request.Composer != ComposerEmpty {
		return WorkerInputPlan{}, ErrComposerNotEmpty
	}
	if err := ValidateWorkerInstruction(request.Instruction); err != nil {
		return WorkerInputPlan{}, err
	}
	if submitKey == "" || settlePause <= 0 || submitAttempts < 2 {
		return WorkerInputPlan{}, errors.New("plan worker instruction: harness delivery contract is incomplete")
	}
	return WorkerInputPlan{
		Kind: WorkerInputInstruction, Text: request.Instruction, SubmitKey: submitKey,
		SettlePause: settlePause, TextAttempts: 1, SubmitAttempts: submitAttempts,
		RequiredComposer: ComposerEmpty, ReconcileWhenUnconfirmed: true,
	}, nil
}

// PlanWorkerControl builds a pause or stop plan. It carries no operator text,
// so there is nothing for a worker to mistake for a message.
func PlanWorkerControl(kind WorkerInputKind, controlKey string, settlePause time.Duration) (WorkerInputPlan, error) {
	if kind != WorkerInputPause && kind != WorkerInputStop {
		return WorkerInputPlan{}, errors.New("plan worker control: kind is not pause or stop")
	}
	if controlKey == "" || settlePause <= 0 {
		return WorkerInputPlan{}, errors.New("plan worker control: harness control contract is incomplete")
	}
	return WorkerInputPlan{
		Kind: kind, ControlKey: controlKey, SettlePause: settlePause,
		TextAttempts: 0, SubmitAttempts: 1, RequiredComposer: ComposerUnknown,
		ReconcileWhenUnconfirmed: true,
	}, nil
}

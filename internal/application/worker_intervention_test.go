package application

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The invariants live in the shared planner, not in each family, so they are
// asserted here directly. A family only supplies its keystrokes and pauses; if
// these rules could be re-decided per adapter, a new harness could quietly
// arrive without them.
func TestPlanWorkerInstruction_RefusesEveryComposerThatIsNotAffirmativelyEmpty(t *testing.T) {
	for _, composer := range []ComposerState{ComposerPending, ComposerUnknown, ComposerState("typing"), ""} {
		_, err := PlanWorkerInstruction(
			WorkerInputRequest{Instruction: "Prefer the smaller change.", Composer: composer},
			"Enter", time.Millisecond, 3,
		)
		if !errors.Is(err, ErrComposerNotEmpty) {
			t.Errorf("PlanWorkerInstruction(composer=%q) error = %v, want ErrComposerNotEmpty", composer, err)
		}
	}
}

func TestPlanWorkerInstruction_TypesOnceAndRetriesOnlyTheSubmission(t *testing.T) {
	plan, err := PlanWorkerInstruction(
		WorkerInputRequest{Instruction: "Prefer the smaller change.", Composer: ComposerEmpty},
		"Enter", 250*time.Millisecond, 3,
	)
	if err != nil {
		t.Fatalf("PlanWorkerInstruction() error = %v", err)
	}
	want := WorkerInputPlan{
		Kind: WorkerInputInstruction, Text: "Prefer the smaller change.", SubmitKey: "Enter",
		SettlePause: 250 * time.Millisecond, TextAttempts: 1, SubmitAttempts: 3,
		RequiredComposer: ComposerEmpty, ReconcileWhenUnconfirmed: true,
	}
	if plan != want {
		t.Fatalf("plan = %#v, want %#v", plan, want)
	}
}

// A family that supplied no submission keystroke, no settle pause, or a
// submission it did not intend to retry has not stated a deliverable contract.
// Refusing here keeps a half-configured harness from delivering anything.
func TestPlanWorkerInstruction_RefusesAnIncompleteHarnessContract(t *testing.T) {
	request := WorkerInputRequest{Instruction: "Prefer the smaller change.", Composer: ComposerEmpty}
	for label, contract := range map[string]struct {
		submitKey string
		pause     time.Duration
		attempts  int
	}{
		"no submit key":  {"", time.Millisecond, 3},
		"no pause":       {"Enter", 0, 3},
		"negative pause": {"Enter", -time.Millisecond, 3},
		"unretryable":    {"Enter", time.Millisecond, 1},
	} {
		if _, err := PlanWorkerInstruction(request, contract.submitKey, contract.pause, contract.attempts); err == nil {
			t.Errorf("PlanWorkerInstruction(%s) error = nil", label)
		}
	}
}

func TestValidateWorkerInstruction_RefusesRatherThanSanitizes(t *testing.T) {
	for label, instruction := range map[string]string{
		"empty": "",
		"blank": "     ",
		// Tab is a completion or focus key in a composer, not text.
		"tab":              "Prefer\tthe smaller change.",
		"newline":          "one\ntwo",
		"carriage return":  "one\rtwo",
		"null":             "one\x00two",
		"escape":           "one\x1b[Atwo",
		"delete":           "one\x7ftwo",
		"other control":    "one\x07two",
		"over the maximum": strings.Repeat("x", MaximumWorkerInstructionBytes+1),
	} {
		if err := ValidateWorkerInstruction(instruction); !errors.Is(err, ErrWorkerInputUnsafe) {
			t.Errorf("ValidateWorkerInstruction(%s) error = %v, want ErrWorkerInputUnsafe", label, err)
		}
	}
	for label, instruction := range map[string]string{
		"plain":          "Prefer the smaller change.",
		"invocation":     "/compact",
		"at the maximum": strings.Repeat("x", MaximumWorkerInstructionBytes),
	} {
		if err := ValidateWorkerInstruction(instruction); err != nil {
			t.Errorf("ValidateWorkerInstruction(%s) error = %v", label, err)
		}
	}
}

func TestPlanWorkerControl_CarriesNoTextAndRefusesAnythingButPauseOrStop(t *testing.T) {
	for _, kind := range []WorkerInputKind{WorkerInputPause, WorkerInputStop} {
		plan, err := PlanWorkerControl(kind, "Escape", time.Millisecond)
		if err != nil {
			t.Fatalf("PlanWorkerControl(%q) error = %v", kind, err)
		}
		if plan.Text != "" || plan.SubmitKey != "" || plan.TextAttempts != 0 {
			t.Errorf("PlanWorkerControl(%q) carries text: %#v", kind, plan)
		}
		if !plan.ReconcileWhenUnconfirmed {
			t.Errorf("PlanWorkerControl(%q) does not reconcile an unconfirmed control", kind)
		}
	}
	for label, attempt := range map[string]struct {
		kind       WorkerInputKind
		controlKey string
		pause      time.Duration
	}{
		"instruction kind": {WorkerInputInstruction, "Escape", time.Millisecond},
		"unknown kind":     {WorkerInputKind("nudge"), "Escape", time.Millisecond},
		"no control key":   {WorkerInputPause, "", time.Millisecond},
		"no pause":         {WorkerInputPause, "Escape", 0},
	} {
		if _, err := PlanWorkerControl(attempt.kind, attempt.controlKey, attempt.pause); err == nil {
			t.Errorf("PlanWorkerControl(%s) error = nil", label)
		}
	}
}

func TestWorkerResumeRequest_ValidateRequiresAnExactHead(t *testing.T) {
	if err := (WorkerResumeRequest{ResumeFromHead: strings.Repeat("b", 40)}).Validate(); err != nil {
		t.Fatalf("Validate(exact head) error = %v", err)
	}
	for label, head := range map[string]string{
		"absent": "", "short": strings.Repeat("b", 39), "long": strings.Repeat("b", 41),
		"uppercase": strings.Repeat("B", 40), "not hex": strings.Repeat("z", 40),
	} {
		if err := (WorkerResumeRequest{ResumeFromHead: head}).Validate(); err == nil {
			t.Errorf("Validate(%s head) error = nil", label)
		}
	}
}

// Absence is unknown, never zero. A zero claims the worker cost nothing, and an
// unattributed one understates whatever ceiling was set against it.
func TestCollectEmittedUsage_ReportsUnknownRatherThanZero(t *testing.T) {
	now := time.Now()
	fresh := func(event string) UsageObservation {
		return UsageObservation{
			EventJSON: []byte(event), ObservedAt: now, Now: now, FreshnessTTL: time.Minute,
		}
	}
	for label, observation := range map[string]UsageObservation{
		"no ttl":      {EventJSON: []byte(`{}`), ObservedAt: now, Now: now},
		"no observed": {EventJSON: []byte(`{}`), Now: now, FreshnessTTL: time.Minute},
		"no now":      {EventJSON: []byte(`{}`), ObservedAt: now, FreshnessTTL: time.Minute},
		"from the future": {
			EventJSON: []byte(`{}`), ObservedAt: now.Add(time.Hour), Now: now, FreshnessTTL: time.Minute,
		},
		"stale": {
			EventJSON:  []byte(`{"usage":{"input_tokens":1,"output_tokens":1}}`),
			ObservedAt: now.Add(-time.Hour), Now: now, FreshnessTTL: time.Minute,
		},
		"no event":     fresh(""),
		"malformed":    fresh("{"),
		"no usage":     fresh(`{"type":"result"}`),
		"half counted": fresh(`{"usage":{"input_tokens":10}}`),
		"negative":     fresh(`{"usage":{"input_tokens":-1,"output_tokens":2}}`),
		"oversized": {
			EventJSON:  []byte(`{"usage":{"input_tokens":1,"output_tokens":` + strings.Repeat("0", MaximumUsageEventBytes) + `}}`),
			ObservedAt: now, Now: now, FreshnessTTL: time.Minute,
		},
	} {
		usage := CollectEmittedUsage(observation)
		if usage.Known || usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.Reason == "" {
			t.Errorf("CollectEmittedUsage(%s) = %#v, want an explained unknown", label, usage)
		}
	}

	usage := CollectEmittedUsage(fresh(`{"usage":{"input_tokens":120,"output_tokens":34}}`))
	if !usage.Known || usage.InputTokens != 120 || usage.OutputTokens != 34 {
		t.Fatalf("CollectEmittedUsage(emitted) = %#v", usage)
	}
}

// A role is what makes a process row read as task state. Anything short of
// exact attribution stays unknown, and the reason names the missing evidence so
// an operator repairs the right thing.
func TestClassifyAttributedProcessRole_RefusesIncompleteEvidence(t *testing.T) {
	complete := TaskProcessObservation{
		TaskHandle: "task-0001", ProfileID: "codex-reviewed", Attributed: true,
		Source: ProcessSourceTerminalDescendant, Executable: "worker",
	}
	if _, ok := ClassifyAttributedProcessRole(complete, "codex-reviewed"); !ok {
		t.Fatal("a complete attributed observation was refused")
	}

	for label, expected := range map[string]struct {
		observation TaskProcessObservation
		reason      ProcessRoleReason
	}{
		"unattributed": {
			TaskProcessObservation{TaskHandle: "task-0001", Source: ProcessSourceTerminalDescendant, Executable: "worker"},
			ProcessRoleReasonUnattributed,
		},
		"no task": {
			TaskProcessObservation{Attributed: true, Source: ProcessSourceTerminalDescendant, Executable: "worker"},
			ProcessRoleReasonMissing,
		},
		"no executable": {
			TaskProcessObservation{TaskHandle: "task-0001", Attributed: true, Source: ProcessSourceTerminalDescendant},
			ProcessRoleReasonMissing,
		},
		"invalid source": {
			TaskProcessObservation{
				TaskHandle: "task-0001", Attributed: true, Executable: "worker",
				Source: ProcessSource("guessed"),
			},
			ProcessRoleReasonMissing,
		},
		"foreign profile": {
			TaskProcessObservation{
				TaskHandle: "task-0001", Attributed: true, Executable: "worker",
				Source: ProcessSourceTerminalDescendant, ProfileID: "someone-elses-profile",
			},
			ProcessRoleReasonForeign,
		},
	} {
		result, ok := ClassifyAttributedProcessRole(expected.observation, "codex-reviewed")
		if ok || result.Role != ProcessRoleUnknown || result.Reason != expected.reason {
			t.Errorf("ClassifyAttributedProcessRole(%s) = %#v, %v; want unknown/%s", label, result, ok, expected.reason)
		}
	}
}

func TestClassifyProcessRoleBySource_NamesOnlyTheRolesTheServiceLaunched(t *testing.T) {
	descendant := TaskProcessObservation{Source: ProcessSourceTerminalDescendant, Executable: "anything"}
	if got := ClassifyProcessRoleBySource(descendant); got.Role != ProcessRoleWorker {
		t.Errorf("terminal descendant role = %q, want worker", got.Role)
	}
	for label, expected := range map[string]ProcessRole{
		"validation":  ProcessRoleValidation,
		"dev-server":  ProcessRoleDevServer,
		"integration": ProcessRoleIntegration,
	} {
		observation := TaskProcessObservation{Source: ProcessSourceServiceLaunched, Executable: label}
		if got := ClassifyProcessRoleBySource(observation); got.Role != expected {
			t.Errorf("service-launched %q role = %q, want %q", label, got.Role, expected)
		}
	}
	unnamed := TaskProcessObservation{Source: ProcessSourceServiceLaunched, Executable: "something-else"}
	got := ClassifyProcessRoleBySource(unnamed)
	if got.Role != ProcessRoleUnknown || got.Reason != ProcessRoleReasonUnsupported {
		t.Errorf("unnamed service operation = %#v, want an unsupported unknown", got)
	}
}

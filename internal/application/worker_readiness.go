package application

// ProcessSource distinguishes how a process came to exist. It is evidence the
// service gathered, never a guess: the two sources have different owners, and a
// role assigned from the wrong one attributes another program's work to a task.
type ProcessSource string

const (
	// ProcessSourceTerminalDescendant is a descendant of the task's terminal.
	ProcessSourceTerminalDescendant ProcessSource = "terminal_descendant"
	// ProcessSourceServiceLaunched is an operation this service started.
	ProcessSourceServiceLaunched ProcessSource = "service_launched"
)

func (source ProcessSource) valid() bool {
	return source == ProcessSourceTerminalDescendant || source == ProcessSourceServiceLaunched
}

// ProcessRole is the closed service classification of one attributed process.
type ProcessRole string

const (
	ProcessRoleWorker      ProcessRole = "worker"
	ProcessRoleValidation  ProcessRole = "validation"
	ProcessRoleDevServer   ProcessRole = "dev_server"
	ProcessRoleIntegration ProcessRole = "integration"
	ProcessRoleUnknown     ProcessRole = "unknown"
)

// ProcessRoleReason names why a role could not be decided. It exists so an
// unknown row says which evidence was missing rather than only that it failed.
type ProcessRoleReason string

const (
	ProcessRoleReasonUnattributed ProcessRoleReason = "unattributed"
	ProcessRoleReasonMissing      ProcessRoleReason = "missing"
	ProcessRoleReasonForeign      ProcessRoleReason = "foreign_profile"
	ProcessRoleReasonUnsupported  ProcessRoleReason = "unsupported"
)

// TaskProcessObservation is the bounded, content-free evidence an adapter may use
// to classify a process. It carries a sanitized executable label only:
// environment and full argv never reach a classification.
type TaskProcessObservation struct {
	TaskHandle string
	ProfileID  string
	Source     ProcessSource
	// Executable is an operator-sanitized label, never a host path.
	Executable string
	// Attributed is true only when an exact process reference was proven,
	// including a start-identity match. Partial attribution stays false.
	Attributed bool
}

// ProcessRoleResult is the classification plus, when unknown, the reason.
type ProcessRoleResult struct {
	Role   ProcessRole
	Reason ProcessRoleReason
}

// HarnessDiagnosis is one family's bounded readiness. It is content-free: it
// carries identities, the pinned version and closed codes, never installed
// paths, credentials or probe output.
type HarnessDiagnosis struct {
	Harness         string
	ProfileID       string
	ExpectedVersion string
	Version         string
	Availability    HarnessAvailability
	Reason          HarnessReason
	// SettleSignalVerified is reported separately from Availability. A harness
	// can be installed, pinned and reachable while still unable to prove a
	// worker turn ended; treating the two as one posture is what would let an
	// unattended profile run on a signal nobody verified.
	SettleSignalVerified bool
	LifecycleReason      HarnessReason
}

// ClassifyAttributedProcessRole is the shared attribution gate every family
// applies before its own role rules run.
//
// Anything short of an exactly attributed observation is unknown. Incomplete
// evidence never becomes a role, because a role is what makes a process row
// look like task state, and the wrong one attributes an unrelated program's
// work — or its termination — to a task.
func ClassifyAttributedProcessRole(observation TaskProcessObservation, ownedProfileID string) (ProcessRoleResult, bool) {
	if !observation.Attributed {
		return ProcessRoleResult{Role: ProcessRoleUnknown, Reason: ProcessRoleReasonUnattributed}, false
	}
	if observation.TaskHandle == "" || observation.Executable == "" || !observation.Source.valid() {
		return ProcessRoleResult{Role: ProcessRoleUnknown, Reason: ProcessRoleReasonMissing}, false
	}
	if observation.ProfileID != "" && observation.ProfileID != ownedProfileID {
		return ProcessRoleResult{Role: ProcessRoleUnknown, Reason: ProcessRoleReasonForeign}, false
	}
	return ProcessRoleResult{}, true
}

// ClassifyProcessRoleBySource applies the role rules shared by every family.
// A terminal descendant is the worker itself; a service-launched operation is
// named by the sanitized label the service gave it when it started it.
func ClassifyProcessRoleBySource(observation TaskProcessObservation) ProcessRoleResult {
	if observation.Source == ProcessSourceTerminalDescendant {
		return ProcessRoleResult{Role: ProcessRoleWorker}
	}
	switch observation.Executable {
	case "validation":
		return ProcessRoleResult{Role: ProcessRoleValidation}
	case "dev-server":
		return ProcessRoleResult{Role: ProcessRoleDevServer}
	case "integration":
		return ProcessRoleResult{Role: ProcessRoleIntegration}
	default:
		return ProcessRoleResult{Role: ProcessRoleUnknown, Reason: ProcessRoleReasonUnsupported}
	}
}

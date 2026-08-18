package application

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var resumeHeadPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// MaximumUsageEventBytes bounds one usage event before it is decoded.
const MaximumUsageEventBytes = 8 * 1024

// WorkerResumeRequest relaunches a paused worker onto the exact tree it left.
//
// E0 resumes through the worktree rather than a vendor session: the work is
// ordinary disk state, so the worker is started again in the same directory and
// told it is continuing. That deliberately avoids depending on a harness's own
// session-resume flag, which each vendor changes on its own schedule and which
// would have to be re-verified per release for a claim this does not need.
type WorkerResumeRequest struct {
	Launch WorkerLaunchRequest
	// ResumeFromHead is the exact revision observed when the task paused.
	// A worker resumed onto a tree that moved would continue against work it
	// never saw and report against a base that no longer exists.
	ResumeFromHead string
}

// Validate rejects a resume that cannot name the tree it is returning to.
func (request WorkerResumeRequest) Validate() error {
	if !resumeHeadPattern.MatchString(request.ResumeFromHead) {
		return errors.New("resume request: head revision is not an exact revision")
	}
	return nil
}

// LifecycleIntegrationRequest asks one family what must exist for the service
// to learn that a worker turn settled.
type LifecycleIntegrationRequest struct {
	ProfileID  string
	TaskHandle string
}

// LifecycleArtifact is one file the service materializes under the task's
// lease root. The path stays relative so path containment remains owned by the
// layer that holds the lease; an adapter never names an absolute destination.
type LifecycleArtifact struct {
	RelativePath string
	Contents     []byte
}

// LifecycleIntegration describes the settle-signal integration for one family
// and states plainly whether that signal was ever verified.
//
// An unverified signal yields no artifacts and a named reason rather than a
// best-effort hook. A hook that looks installed but emits nothing is worse than
// none: it reads as proof that a turn ended, which is exactly the claim the
// service must never invent.
type LifecycleIntegration struct {
	Harness              string
	Artifacts            []LifecycleArtifact
	SettleSignalVerified bool
	Reason               HarnessReason
}

// UsageObservation is bounded harness-emitted usage evidence.
type UsageObservation struct {
	ProfileID    string
	TaskHandle   string
	EventJSON    []byte
	ObservedAt   time.Time
	Now          time.Time
	FreshnessTTL time.Duration
}

// WorkerUsage is attributable usage, or an explicit statement that there is
// none. Counts are meaningful only when Known is true.
type WorkerUsage struct {
	Known        bool
	InputTokens  int64
	OutputTokens int64
	Reason       SemanticReason
}

// CollectEmittedUsage reads usage a harness actually emitted.
//
// Absence, staleness and malformed evidence are unknown, never zero. A zero is
// a claim that the worker cost nothing, and an unattributed one silently
// understates whatever ceiling an operator set against it.
func CollectEmittedUsage(observation UsageObservation) WorkerUsage {
	if observation.FreshnessTTL <= 0 || observation.ObservedAt.IsZero() || observation.Now.IsZero() ||
		observation.ObservedAt.After(observation.Now) {
		return WorkerUsage{Reason: SemanticReasonMissing}
	}
	if observation.Now.Sub(observation.ObservedAt) > observation.FreshnessTTL {
		return WorkerUsage{Reason: SemanticReasonStale}
	}
	if len(observation.EventJSON) == 0 {
		return WorkerUsage{Reason: SemanticReasonMissing}
	}
	if len(observation.EventJSON) > MaximumUsageEventBytes {
		return WorkerUsage{Reason: SemanticReasonMalformed}
	}
	var event struct {
		Usage *struct {
			InputTokens  *int64 `json:"input_tokens"`
			OutputTokens *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(observation.EventJSON, &event); err != nil {
		return WorkerUsage{Reason: SemanticReasonMalformed}
	}
	if event.Usage == nil {
		return WorkerUsage{Reason: SemanticReasonMissing}
	}
	if event.Usage.InputTokens == nil || event.Usage.OutputTokens == nil {
		// A half-reported turn is not a cheaper turn.
		return WorkerUsage{Reason: SemanticReasonMalformed}
	}
	if *event.Usage.InputTokens < 0 || *event.Usage.OutputTokens < 0 {
		return WorkerUsage{Reason: SemanticReasonMalformed}
	}
	return WorkerUsage{
		Known: true, InputTokens: *event.Usage.InputTokens, OutputTokens: *event.Usage.OutputTokens,
	}
}

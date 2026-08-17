package comiswire

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// HeartbeatSender proves to the host that this service still holds one run.
type HeartbeatSender interface {
	Heartbeat(context.Context, HeartbeatRequestParams) (HeartbeatResponseResult, error)
}

// LivenessTaskReader reads the durable task records a sweep beats for. It is
// declared here rather than shared with the query layer because liveness needs
// exactly two facts per task — whether it is bound and whether it has settled.
type LivenessTaskReader interface {
	ListTasks(context.Context) ([]domain.Task, error)
}

// LivenessReporterConfig composes durable task state with the authenticated
// control connection and the sweep interval.
type LivenessReporterConfig struct {
	Tasks    LivenessTaskReader
	Sender   HeartbeatSender
	Clock    application.Clock
	Interval time.Duration
	// Bound on one beat. Absent, it defaults to the sweep interval, so a stalled
	// connection can never delay the next sweep by more than one period.
	BeatTimeout time.Duration
}

// LivenessReporter beats once per sweep for every bound, unsettled run.
//
// A heartbeat carries no run state. It answers one question the host cannot
// answer for itself: whether the service that owns a run is still there. Without
// it the host cannot tell a service that is working quietly from one that died,
// so it reduces the run to unknown — which is correct, and which only a beat can
// change.
type LivenessReporter struct {
	config LivenessReporterConfig
}

// NewLivenessReporter validates liveness dependencies before supervision.
func NewLivenessReporter(config LivenessReporterConfig) (*LivenessReporter, error) {
	if config.Tasks == nil {
		return nil, errors.New("create Comis liveness reporter: task reader is required")
	}
	if config.Sender == nil {
		return nil, errors.New("create Comis liveness reporter: sender is required")
	}
	if config.Clock == nil {
		return nil, errors.New("create Comis liveness reporter: clock is required")
	}
	if config.Interval <= 0 {
		return nil, errors.New("create Comis liveness reporter: interval must be positive")
	}
	if config.BeatTimeout <= 0 {
		config.BeatTimeout = config.Interval
	}
	return &LivenessReporter{config: config}, nil
}

// Run sweeps durable task state on the configured interval. Cancellation joins
// the loop; only a task-store failure stops supervision, because that means the
// reporter can no longer tell which runs it is meant to be proving.
func (reporter *LivenessReporter) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run Comis liveness reporter: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		tasks, err := reporter.config.Tasks.ListTasks(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("run Comis liveness reporter: read tasks: %w", err)
		}
		observed := reporter.config.Clock().UTC()
		for _, task := range tasks {
			if !livenessRunIsWorthBeating(task) {
				continue
			}
			// Each beat carries its own deadline. The control connection waits
			// for an authenticated session, so a beat sent while it is
			// reconnecting would otherwise hold the whole sweep — and a beat
			// delayed past the host's staleness bound proves nothing anyway.
			//
			// A refusal is the host's answer about this one run — terminal, not
			// ours, or an observation it already holds. None of those is a
			// reason to stop proving that the remaining runs are alive, and a
			// beat is never retried: the next sweep carries a fresher one.
			beatCtx, releaseBeat := context.WithTimeout(ctx, reporter.config.BeatTimeout)
			_, err := reporter.config.Sender.Heartbeat(beatCtx, HeartbeatRequestParams{
				OperationID:  OperationID(livenessOperationID(task, observed)),
				ManagedRunID: ManagedRunID(task.ManagedRunID),
				ObservedAtMs: observed.UnixMilli(),
			})
			releaseBeat()
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
		if err := waitControlBackoff(ctx, reporter.config.Interval); err != nil {
			return err
		}
	}
}

// A run is worth beating for when the host still holds it open on our behalf: it
// has a managed run bound, and it has not reached a state the host has settled.
func livenessRunIsWorthBeating(task domain.Task) bool {
	if task.ManagedRunID == "" {
		return false
	}
	switch task.State {
	case domain.TaskDelivered, domain.TaskFailed, domain.TaskCancelled,
		domain.TaskCleaned, domain.TaskCleanupHeld:
		return false
	default:
		return true
	}
}

// The operation identity is derived from the run and the observation instant, so
// a repeated sweep at the same instant is one logical beat while the next sweep
// is a distinct one.
func livenessOperationID(task domain.Task, observed time.Time) string {
	return fmt.Sprintf("liveness-%s-%d", task.ManagedRunID, observed.UnixMilli())
}

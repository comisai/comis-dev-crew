package workers

import (
	"context"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// resumeBootstrap tells a relaunched worker that the tree already holds its own
// unfinished work and names the exact head it left. Without it the worker reads
// the launch bootstrap and starts over on top of that work.
func resumeBootstrap(head string) string {
	return fmt.Sprintf(
		"You are resuming existing work in this worktree, not starting it. The tree is exactly as you "+
			"left it at revision %s; do not reset, discard, or recreate it. Before continuing, acknowledge "+
			"the exact protected launch binding with `devcrew-report acknowledge`, then re-read the pinned "+
			"task brief with `devcrew-report brief`. If either command fails, stop without reading or "+
			"changing the workspace. Run `devcrew-report --help` before reporting and use only its exact "+
			"flag syntax. Treat the protected runtime attachment as the only task/report authority.\n",
		head,
	)
}

// BuildResumeDescriptor relaunches a paused Codex worker onto the tree it left.
func (adapter *CodexAdapter) BuildResumeDescriptor(
	ctx context.Context,
	request application.WorkerResumeRequest,
) (application.WorkerLaunchDescriptor, error) {
	return buildResumeDescriptor(ctx, adapter.BuildLaunchDescriptor, request)
}

// InstallLifecycleIntegration states what the Codex family needs before a
// worker turn's end is an authenticated fact.
func (adapter *CodexAdapter) InstallLifecycleIntegration(
	ctx context.Context,
	request application.LifecycleIntegrationRequest,
) (application.LifecycleIntegration, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.LifecycleIntegration{}, err
	}
	return lifecycleIntegration(adapter.ID(), adapter.settleSignalVerified), nil
}

// CollectUsage reads usage the Codex worker actually emitted.
func (adapter *CodexAdapter) CollectUsage(
	ctx context.Context,
	observation application.UsageObservation,
) (application.WorkerUsage, error) {
	if err := adapter.readyFor(ctx, observation.ProfileID); err != nil {
		return application.WorkerUsage{}, err
	}
	return application.CollectEmittedUsage(observation), nil
}

// BuildResumeDescriptor relaunches a paused Claude Code worker onto the tree it
// left.
func (adapter *ClaudeAdapter) BuildResumeDescriptor(
	ctx context.Context,
	request application.WorkerResumeRequest,
) (application.WorkerLaunchDescriptor, error) {
	return buildResumeDescriptor(ctx, adapter.BuildLaunchDescriptor, request)
}

// InstallLifecycleIntegration states what the Claude Code family needs before a
// worker turn's end is an authenticated fact.
func (adapter *ClaudeAdapter) InstallLifecycleIntegration(
	ctx context.Context,
	request application.LifecycleIntegrationRequest,
) (application.LifecycleIntegration, error) {
	if err := adapter.readyFor(ctx, request.ProfileID); err != nil {
		return application.LifecycleIntegration{}, err
	}
	return lifecycleIntegration(adapter.ID(), adapter.settleSignalVerified), nil
}

// CollectUsage reads usage the Claude Code worker actually emitted.
func (adapter *ClaudeAdapter) CollectUsage(
	ctx context.Context,
	observation application.UsageObservation,
) (application.WorkerUsage, error) {
	if err := adapter.readyFor(ctx, observation.ProfileID); err != nil {
		return application.WorkerUsage{}, err
	}
	return application.CollectEmittedUsage(observation), nil
}

// buildResumeDescriptor reuses the family's own reviewed launch descriptor and
// replaces only its trailing bootstrap. The executable, argument vector,
// environment allowlist and attachment binding stay exactly what launch
// reviewed, so resuming cannot become a second, less-examined way to start a
// worker.
func buildResumeDescriptor(
	ctx context.Context,
	buildLaunch func(context.Context, application.WorkerLaunchRequest) (application.WorkerLaunchDescriptor, error),
	request application.WorkerResumeRequest,
) (application.WorkerLaunchDescriptor, error) {
	if err := request.Validate(); err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	descriptor, err := buildLaunch(ctx, request.Launch)
	if err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	if len(descriptor.Arguments) == 0 {
		return application.WorkerLaunchDescriptor{}, errArgumentsMissing
	}
	arguments := append([]string(nil), descriptor.Arguments...)
	arguments[len(arguments)-1] = resumeBootstrap(request.ResumeFromHead)
	descriptor.Arguments = arguments
	return descriptor, nil
}

// lifecycleIntegration reports the settle-signal posture for one family.
//
// An unverified signal returns no artifacts. A hook that looks installed but
// emits nothing is worse than none: the service would read its silence as proof
// that a turn ended, which is the one claim it must never invent.
func lifecycleIntegration(harness string, settleSignalVerified bool) application.LifecycleIntegration {
	integration := application.LifecycleIntegration{
		Harness: harness, SettleSignalVerified: settleSignalVerified,
	}
	if !settleSignalVerified {
		integration.Reason = application.HarnessReasonLifecycleSignalUnknown
	}
	return integration
}

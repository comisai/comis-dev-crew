package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

type fixtureSupervisorStore interface {
	ListTasks(context.Context) ([]domain.Task, error)
	application.ReportMutationStore
}

type fixtureTaskStarter interface {
	StartTask(context.Context, application.StartTaskCommand) (application.MutationResult, error)
}

type fixtureSupervisorConfig struct {
	Store         fixtureSupervisorStore
	Mutations     fixtureTaskStarter
	Clock         application.Clock
	Decision      string
	PollInterval  time.Duration
	NewCredential func() (string, error)
}

type fixtureSupervisor struct {
	store         fixtureSupervisorStore
	mutations     fixtureTaskStarter
	clock         application.Clock
	decision      string
	pollInterval  time.Duration
	newCredential func() (string, error)
}

func newFixtureSupervisor(config fixtureSupervisorConfig) (*fixtureSupervisor, error) {
	if config.Store == nil || config.Mutations == nil || config.Clock == nil || config.NewCredential == nil {
		return nil, errors.New("create fixture supervisor: store, mutations, clock, and credential source are required")
	}
	if config.Decision == "" || config.PollInterval <= 0 {
		return nil, errors.New("create fixture supervisor: decision and positive poll interval are required")
	}
	return &fixtureSupervisor{
		store: config.Store, mutations: config.Mutations, clock: config.Clock,
		decision: config.Decision, pollInterval: config.PollInterval,
		newCredential: config.NewCredential,
	}, nil
}

func (supervisor *fixtureSupervisor) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run fixture supervisor: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for {
		ran, err := supervisor.runNext(ctx)
		if err != nil {
			return err
		}
		if ran {
			continue
		}
		timer := time.NewTimer(supervisor.pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (supervisor *fixtureSupervisor) runNext(ctx context.Context) (bool, error) {
	tasks, err := supervisor.store.ListTasks(ctx)
	if err != nil {
		return false, fmt.Errorf("fixture supervisor list tasks: %w", err)
	}
	for _, task := range tasks {
		if task.State != domain.TaskReady || task.WorkerProfileID != "fixture-worker" {
			continue
		}
		if err := supervisor.runFixture(ctx, task); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func (supervisor *fixtureSupervisor) runFixture(ctx context.Context, ready domain.Task) error {
	started, err := supervisor.mutations.StartTask(ctx, application.StartTaskCommand{
		OperationID: fixtureStartOperationID(ready.Handle), TaskHandle: ready.Handle,
	})
	if err != nil {
		return fmt.Errorf("fixture supervisor record launch intent: %w", err)
	}
	brief, err := started.Task.RenderWorkerBrief()
	if err != nil {
		return fmt.Errorf("fixture supervisor render pinned brief: %w", err)
	}
	sink, err := application.NewReportSink(application.ReportSinkConfig{Store: supervisor.store, Clock: supervisor.clock})
	if err != nil {
		return fmt.Errorf("fixture supervisor report sink: %w", err)
	}
	credential, err := supervisor.newCredential()
	if err != nil {
		return fmt.Errorf("fixture supervisor reporter credential: %w", err)
	}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: started.Task.Handle, BriefRevision: started.Task.BriefRevision,
		BriefRevisionHash: started.Task.BriefRevisionHash, Credential: credential, Sink: sink,
	})
	if err != nil {
		return fmt.Errorf("fixture supervisor reporter endpoint: %w", err)
	}
	client, err := reporter.NewClient(endpoint, credential)
	if err != nil {
		return fmt.Errorf("fixture supervisor reporter client: %w", err)
	}
	fixture, err := workers.NewFixture(workers.FixtureConfig{
		Brief: brief, Reporter: client, Decisions: fixedFixtureDecision(supervisor.decision),
		Clock: workers.Clock(supervisor.clock), ReportIDPrefix: fixtureReportPrefix(started.Task.Handle),
		DecisionKey: fixtureDecisionKey(started.Task.Handle), Fault: workers.FaultNone,
	})
	if err != nil {
		return fmt.Errorf("fixture supervisor worker: %w", err)
	}
	if err := fixture.Run(ctx); err != nil {
		return fmt.Errorf("fixture supervisor run worker: %w", err)
	}
	return nil
}

type fixedFixtureDecision string

func (decision fixedFixtureDecision) AwaitDecision(context.Context, string) (string, error) {
	return string(decision), nil
}

func fixtureStartOperationID(taskHandle string) string {
	return "start-" + fixtureIdentityDigest(taskHandle)
}

func fixtureReportPrefix(taskHandle string) string {
	return "fixture-" + fixtureIdentityDigest(taskHandle)
}

func fixtureDecisionKey(taskHandle string) string {
	return "decision-" + fixtureIdentityDigest(taskHandle)
}

func fixtureIdentityDigest(taskHandle string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(taskHandle)))
	return digest[:24]
}

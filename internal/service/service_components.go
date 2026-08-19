package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

func serveServiceComponents(
	ctx context.Context,
	servers []*localapi.Server,
	components []func(context.Context) error,
	beforeReady func(context.Context) error,
	ready func(),
) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	if len(servers) != 0 {
		components = append([]func(context.Context) error{
			func(componentContext context.Context) error { return serveLocalEndpoints(componentContext, servers) },
		}, components...)
	}
	if len(components) == 0 {
		return errors.New("service component stopped unexpectedly")
	}
	results := make(chan error, len(components))
	for _, component := range components {
		go func(run func(context.Context) error) { results <- run(runContext) }(component)
	}
	readiness := make(chan error, 1)
	if beforeReady == nil {
		readiness <- nil
	} else {
		go func() { readiness <- beforeReady(runContext) }()
	}
	var resultErr error
	remaining := len(components)
	select {
	case err := <-readiness:
		if err != nil {
			if !(errors.Is(err, context.Canceled) && runContext.Err() != nil) {
				resultErr = errors.Join(resultErr, err)
			}
			cancel()
			for _, server := range servers {
				resultErr = errors.Join(resultErr, server.Close())
			}
		} else if ctx.Err() == nil && runContext.Err() == nil {
			if ready != nil {
				ready()
			}
		}
	case err := <-results:
		remaining--
		if err == nil && ctx.Err() == nil && runContext.Err() == nil {
			resultErr = errors.Join(resultErr, errors.New("service component stopped unexpectedly"))
		} else if err != nil && !(errors.Is(err, context.Canceled) && runContext.Err() != nil) {
			resultErr = errors.Join(resultErr, err)
		}
		cancel()
		for _, server := range servers {
			resultErr = errors.Join(resultErr, server.Close())
		}
	}
	for ; remaining > 0; remaining-- {
		err := <-results
		if err == nil && ctx.Err() == nil && runContext.Err() == nil {
			resultErr = errors.Join(resultErr, errors.New("service component stopped unexpectedly"))
		} else if err != nil && !(errors.Is(err, context.Canceled) && runContext.Err() != nil) {
			resultErr = errors.Join(resultErr, err)
		}
		cancel()
		for _, server := range servers {
			resultErr = errors.Join(resultErr, server.Close())
		}
	}
	return resultErr
}

// composeRuntimeAttachments installs the service-owned attachment coordinator
// and recovers relay identities it already owns. An injected coordinator keeps
// its own authority, so a durable upgrade recorded against a coordinator this
// process does not own is refused rather than silently skipped.
func composeRuntimeAttachments(
	ctx context.Context,
	config *Config,
	store runtimeAttachmentStore,
	clock application.Clock,
) (*runtimeAttachmentCoordinator, error) {
	if config.RuntimeAttachments != nil || config.RuntimeRoot == "" {
		upgrades, err := store.ListRuntimeRelayIdentityUpgrades(ctx)
		if err != nil || len(upgrades) != 0 {
			return nil, errors.New("run service runtime relay identity upgrade requires service-owned attachments")
		}
		return nil, nil
	}
	supervisor, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: config.RuntimeRoot, Store: store, Clock: clock,
		NewCredential:           func() (string, error) { return randomIdentity("runtime-credential", 16) },
		NewAttentionOperationID: func() (string, error) { return randomIdentity("attention-response", 16) },
	})
	if err != nil {
		return nil, fmt.Errorf("run service runtime attachments: %w", err)
	}
	config.RuntimeAttachments = supervisor
	if err := supervisor.recoverRuntimeRelayIdentityUpgrades(ctx); err != nil {
		return nil, fmt.Errorf("run service runtime relay identity upgrade: %w", err)
	}
	return supervisor, nil
}

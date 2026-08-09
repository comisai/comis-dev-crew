// Package service composes the sole durable daemon authority.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

const (
	comisReportPollInterval   = 250 * time.Millisecond
	comisReportMinimumBackoff = 100 * time.Millisecond
	comisReportMaximumBackoff = 5 * time.Second
)

// ComisControl is the single persistent authenticated connection supervised
// by the service. The concrete control adapter also carries durable reports.
type ComisControl interface {
	comiswire.ReportSender
	Run(context.Context) error
}

// Config identifies the service-owned database and operator endpoint.
type Config struct {
	DatabasePath           string
	SocketPath             string
	MCPSocketPath          string
	ServiceInstanceID      string
	Repositories           application.RepositoryCatalog
	TaskIDs                application.TaskIDSource
	RegistrationNonces     application.RegistrationNonceSource
	RequestedWorkspaceRoot string
	PreparationTTL         time.Duration
	Clock                  application.Clock
	ComisControl           ComisControl
	Ready                  func()
}

// Run opens the sole writable store and serves canonical operator queries until
// cancellation. It joins every acquired resource before returning.
func Run(ctx context.Context, config Config) (resultErr error) {
	if ctx == nil {
		return errors.New("run service: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.DatabasePath == "" || config.SocketPath == "" {
		return errors.New("run service: database and socket paths are required")
	}
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	store, err := sqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		return fmt.Errorf("run service store: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, store.Close())
	}()
	reconciler, err := application.NewStartupReconciler(application.StartupReconcilerConfig{Store: store, Clock: clock})
	if err != nil {
		return fmt.Errorf("run service startup reconciler: %w", err)
	}
	if _, err := reconciler.Reconcile(ctx); err != nil {
		return fmt.Errorf("run service startup reconciliation: %w", err)
	}
	queries, err := application.NewQueries(store, clock)
	if err != nil {
		return fmt.Errorf("run service queries: %w", err)
	}
	mutations, err := composeMutations(config, store, clock)
	if err != nil {
		return err
	}
	handlerConfig := localapi.HandlerConfig{Queries: queries, Clock: clock}
	if mutations != nil {
		handlerConfig.Mutations = mutations
		handlerConfig.ServiceInstanceID = config.ServiceInstanceID
	}
	handler, err := localapi.NewHandler(handlerConfig)
	if err != nil {
		return fmt.Errorf("run service local handler: %w", err)
	}
	operatorServer, err := localapi.Listen(config.SocketPath, localapi.CallerOperatorCLI, handler)
	if err != nil {
		return fmt.Errorf("run service local endpoint: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, operatorServer.Close())
	}()
	servers := []*localapi.Server{operatorServer}
	if config.MCPSocketPath != "" {
		mcpServer, listenErr := localapi.Listen(config.MCPSocketPath, localapi.CallerMCPFacade, handler)
		if listenErr != nil {
			return fmt.Errorf("run service MCP endpoint: %w", listenErr)
		}
		defer func() { resultErr = errors.Join(resultErr, mcpServer.Close()) }()
		servers = append(servers, mcpServer)
	}
	if config.ComisControl == nil {
		if config.Ready != nil {
			config.Ready()
		}
		return serveLocalEndpoints(ctx, servers)
	}
	forwarder, err := comiswire.NewReportForwarder(comiswire.ReportForwarderConfig{
		Outbox: store, Sender: config.ComisControl, Clock: clock,
		PollInterval: comisReportPollInterval, MinimumBackoff: comisReportMinimumBackoff,
		MaximumBackoff: comisReportMaximumBackoff,
	})
	if err != nil {
		return fmt.Errorf("run service Comis report forwarder: %w", err)
	}
	return serveComisComponents(ctx, servers, config.ComisControl, forwarder, config.Ready)
}

func composeMutations(config Config, store *sqlite.Store, clock application.Clock) (*application.Mutations, error) {
	configured := config.Repositories != nil || config.TaskIDs != nil || config.RegistrationNonces != nil ||
		config.RequestedWorkspaceRoot != "" || config.PreparationTTL != 0 || config.ServiceInstanceID != ""
	if !configured {
		if config.MCPSocketPath != "" {
			return nil, errors.New("run service: MCP endpoint requires mutation configuration")
		}
		return nil, nil
	}
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: config.Repositories, TaskIDs: config.TaskIDs,
		RegistrationNonces: config.RegistrationNonces, RequestedWorkspaceRoot: config.RequestedWorkspaceRoot,
		PreparationTTL: config.PreparationTTL,
		Clock:          clock,
	})
	if err != nil {
		return nil, fmt.Errorf("run service mutation coordinator: %w", err)
	}
	if config.ServiceInstanceID == "" {
		return nil, errors.New("run service: mutation service instance is required")
	}
	return mutations, nil
}

func serveLocalEndpoints(ctx context.Context, servers []*localapi.Server) error {
	if len(servers) == 1 {
		return servers[0].Serve(ctx)
	}
	serveContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(servers))
	for _, server := range servers {
		go func(endpoint *localapi.Server) { results <- endpoint.Serve(serveContext) }(server)
	}
	var resultErr error
	for range servers {
		err := <-results
		resultErr = errors.Join(resultErr, err)
		cancel()
		for _, server := range servers {
			resultErr = errors.Join(resultErr, server.Close())
		}
	}
	return resultErr
}

func serveComisComponents(
	ctx context.Context,
	servers []*localapi.Server,
	control ComisControl,
	forwarder *comiswire.ReportForwarder,
	ready func(),
) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	components := []func(context.Context) error{
		func(componentContext context.Context) error { return serveLocalEndpoints(componentContext, servers) },
		control.Run,
		forwarder.Run,
	}
	results := make(chan error, len(components))
	for _, component := range components {
		go func(run func(context.Context) error) { results <- run(runContext) }(component)
	}
	if ready != nil {
		ready()
	}
	var resultErr error
	for range components {
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

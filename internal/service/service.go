// Package service composes the sole durable daemon authority.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

// Config identifies the service-owned database and operator endpoint.
type Config struct {
	DatabasePath string
	SocketPath   string
	Clock        application.Clock
	Ready        func()
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
		clock = time.Now
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
	handler, err := localapi.NewHandler(queries, clock)
	if err != nil {
		return fmt.Errorf("run service local handler: %w", err)
	}
	server, err := localapi.Listen(config.SocketPath, localapi.CallerOperatorCLI, handler)
	if err != nil {
		return fmt.Errorf("run service local endpoint: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, server.Close())
	}()
	if config.Ready != nil {
		config.Ready()
	}
	return server.Serve(ctx)
}

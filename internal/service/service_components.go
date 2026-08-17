package service

import (
	"context"
	"errors"

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

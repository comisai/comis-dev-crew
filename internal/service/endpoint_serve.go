package service

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/localapi"
)

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

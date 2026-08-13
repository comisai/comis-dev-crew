package livecampaign

import (
	"context"
	"errors"
)

type RealExecutor struct{}

func (RealExecutor) Run(context.Context, Command) ([]byte, error) {
	return nil, errors.New("protected command execution not implemented")
}

func ValidateRuntime(Manifest) error {
	return errors.New("protected runtime validation not implemented")
}

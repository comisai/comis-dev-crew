package livecampaign

import (
	"context"
	"errors"
	"time"
)

type CampaignRunner struct {
	Executor     Executor
	PollInterval time.Duration
	NowMs        func() int64
	Logf         func(string, ...any)
}

func (CampaignRunner) Run(context.Context, Manifest, string) (Verdict, error) {
	return Verdict{}, errors.New("protected live campaign runner not implemented")
}

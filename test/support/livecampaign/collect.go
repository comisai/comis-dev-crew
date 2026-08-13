package livecampaign

import (
	"context"
	"errors"
)

type Command struct {
	Path string
	Args []string
	Env  map[string]string
}

type Executor interface {
	Run(context.Context, Command) ([]byte, error)
}

type EvidenceCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Verdict struct {
	SchemaVersion     int             `json:"schemaVersion"`
	CampaignID        string          `json:"campaignId"`
	CapturedAtMs      int64           `json:"capturedAtMs"`
	Passed            bool            `json:"passed"`
	EvidenceDirectory string          `json:"evidenceDirectory"`
	Checks            []EvidenceCheck `json:"checks"`
}

func Collect(context.Context, Manifest, string, Executor, int64) (Verdict, error) {
	return Verdict{}, errors.New("live closeout collection not implemented")
}

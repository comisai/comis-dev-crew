package service

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
)

const (
	comisReportPollInterval   = 250 * time.Millisecond
	comisReportMinimumBackoff = 100 * time.Millisecond
	comisReportMaximumBackoff = 5 * time.Second
	comisRequestTimeout       = 5 * time.Second
	// Well inside the host's own staleness bound, so one missed sweep — a slow
	// store read, a reconnect — never makes a healthy service look departed.
	comisLivenessInterval = 60 * time.Second
	comisMinimumBackoff   = 100 * time.Millisecond
	comisMaximumBackoff   = time.Second
	fixturePollInterval   = 25 * time.Millisecond
)

// ComisControl is the single persistent authenticated connection supervised
// by the service. The concrete control adapter also carries durable reports.
type ComisControl interface {
	comiswire.ReportSender
	comiswire.EvidenceSender
	comiswire.HeartbeatSender
	comiswire.AttentionResponseReceiver
	application.InitiativeHostRollupSource
	application.ManagedRunReleaser
	application.HostIntegrationStatus
	Run(context.Context) error
}

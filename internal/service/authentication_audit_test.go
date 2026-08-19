package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

// TestService_EveryProductionReporterEndpointCanAudit keeps the two endpoint
// construction sites honest.
//
// The endpoint refuses to exist without an auditor, so a missing one is caught
// at construction — but only for a path a test actually exercises. Both
// production sites build their endpoint deep inside a supervisor, and the
// fixture lane is exactly where an unreviewable authentication boundary would
// sit unnoticed, because the deterministic worker never presents a bad
// credential.
func TestService_EveryProductionReporterEndpointCanAudit(t *testing.T) {
	var _ reporter.AuthenticationAuditor = (*runtimeAttachmentCoordinator)(nil)
	var _ reporter.AuthenticationAuditor = (*fixtureSupervisor)(nil)
}

// TestService_AuditedRejectionNamesTheTaskAndGround proves the adapters record
// the closed vocabulary rather than whatever the caller happened to pass.
func TestService_AuditedRejectionNamesTheTaskAndGround(t *testing.T) {
	recorder := &capturingAuditRecorder{}
	coordinator := &runtimeAttachmentCoordinator{store: capturingAttachmentStore{recorder: recorder}, clock: fixedAuditClock}
	if err := coordinator.RecordReportAuthenticationFailure(context.Background(), "task-0001"); err != nil {
		t.Fatalf("RecordReportAuthenticationFailure() error = %v", err)
	}
	supervisor := &fixtureSupervisor{store: capturingFixtureStore{recorder: recorder}, clock: fixedAuditClock}
	if err := supervisor.RecordReportAuthenticationFailure(context.Background(), "task-0002"); err != nil {
		t.Fatalf("RecordReportAuthenticationFailure() error = %v", err)
	}
	if len(recorder.events) != 2 {
		t.Fatalf("recorded %d events, want 2", len(recorder.events))
	}
	for index, want := range []string{"task-0001", "task-0002"} {
		event := recorder.events[index]
		if event.Kind != application.AuditReportAuthenticationFailed {
			t.Errorf("event %d kind = %q, want an authentication failure", index, event.Kind)
		}
		if event.Reason != application.AuditCredentialMismatch {
			t.Errorf("event %d reason = %q, want a credential mismatch", index, event.Reason)
		}
		if event.TaskHandle != want {
			t.Errorf("event %d task = %q, want %q", index, event.TaskHandle, want)
		}
		if err := event.Validate(); err != nil {
			t.Errorf("event %d is not a recordable audit event: %v", index, err)
		}
	}
}

// TestService_AuditedRejectionCarriesNoCredentialMaterial guards the one thing
// this record must never hold: the authority that was presented.
func TestService_AuditedRejectionCarriesNoCredentialMaterial(t *testing.T) {
	recorder := &capturingAuditRecorder{}
	coordinator := &runtimeAttachmentCoordinator{store: capturingAttachmentStore{recorder: recorder}, clock: fixedAuditClock}
	if err := coordinator.RecordReportAuthenticationFailure(context.Background(), "task-0001"); err != nil {
		t.Fatalf("RecordReportAuthenticationFailure() error = %v", err)
	}
	rendered := strings.Join([]string{
		string(recorder.events[0].Kind), string(recorder.events[0].Reason), recorder.events[0].TaskHandle,
	}, " ")
	if strings.Contains(rendered, "credential-") || strings.Contains(rendered, "secret") {
		t.Errorf("audit record carried credential material: %q", rendered)
	}
}

// capturingAuditRecorder collects the records both adapters emit. It embeds the
// narrower store each adapter needs separately, because embedding both in one
// type makes their shared methods ambiguous.
type capturingAuditRecorder struct {
	events []application.AuditEvent
}

func (recorder *capturingAuditRecorder) record(event application.AuditEvent) error {
	recorder.events = append(recorder.events, event)
	return nil
}

type capturingAttachmentStore struct {
	runtimeAttachmentStore
	recorder *capturingAuditRecorder
}

func (store capturingAttachmentStore) RecordAuditEvent(_ context.Context, event application.AuditEvent) error {
	return store.recorder.record(event)
}

type capturingFixtureStore struct {
	fixtureSupervisorStore
	recorder *capturingAuditRecorder
}

func (store capturingFixtureStore) RecordAuditEvent(_ context.Context, event application.AuditEvent) error {
	return store.recorder.record(event)
}

func fixedAuditClock() time.Time {
	return time.Date(2026, time.August, 19, 9, 0, 0, 0, time.UTC)
}

// RecordAuditEvent keeps the recovery store usable as a runtimeAttachmentStore.
// It lives beside the audit tests rather than with the recovery fixtures so the
// audit surface's scaffolding stays in one place.
func (store *runtimeAttachmentRecoveryStore) RecordAuditEvent(context.Context, application.AuditEvent) error {
	return nil
}

package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type auditReaderStub struct {
	events []AuditEvent
	err    error
}

func (reader *auditReaderStub) ReadAuditEvents(_ context.Context, after int64, limit int) ([]AuditEvent, error) {
	if reader.err != nil {
		return nil, reader.err
	}
	var page []AuditEvent
	for _, event := range reader.events {
		if event.Sequence > after && len(page) < limit {
			page = append(page, event)
		}
	}
	return page, nil
}

func auditQueries(t *testing.T, reader AuditReader) *Queries {
	t.Helper()
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{}, Clock: time.Now, Audit: reader,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	return queries
}

func TestQueries_ReadAuditBoundsPagesAndAlwaysReturnsACursor(t *testing.T) {
	observed := time.Now().UTC()
	reader := &auditReaderStub{events: []AuditEvent{
		{Sequence: 1, OccurredAt: observed, Kind: AuditCleanupRefused, Reason: AuditCleanupOpenHold},
		{Sequence: 2, OccurredAt: observed, Kind: AuditReportAuthenticationFailed, Reason: AuditCredentialMismatch},
	}}
	queries := auditQueries(t, reader)

	page, err := queries.ReadAudit(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("ReadAudit() error = %v", err)
	}
	if len(page.Events) != 2 || page.NextCursor != 2 || page.SchemaVersion != 1 {
		t.Fatalf("ReadAudit(default limit) = %#v", page)
	}
	if capped, err := queries.ReadAudit(context.Background(), 0, MaximumAuditPage+50); err != nil || len(capped.Events) != 2 {
		t.Fatalf("ReadAudit(oversized limit) = %#v, %v", capped, err)
	}
	// An exhausted cursor must come back, or a reader cannot tell "nothing
	// happened" from "I lost my place".
	empty, err := queries.ReadAudit(context.Background(), 2, 10)
	if err != nil {
		t.Fatalf("ReadAudit(exhausted) error = %v", err)
	}
	if len(empty.Events) != 0 || empty.NextCursor != 2 {
		t.Fatalf("ReadAudit(exhausted) = %#v, want an empty page holding its cursor", empty)
	}
}

func TestQueries_ReadAuditRefusesUnusableCursorsAndUnavailableTrails(t *testing.T) {
	queries := auditQueries(t, &auditReaderStub{})
	if _, err := queries.ReadAudit(context.Background(), -1, 10); err == nil {
		t.Error("ReadAudit() accepted a negative cursor")
	}
	failing := auditQueries(t, &auditReaderStub{err: errors.New("trail unavailable")})
	if _, err := failing.ReadAudit(context.Background(), 0, 10); err == nil {
		t.Error("ReadAudit() hid an unreadable trail")
	}
	absent, err := NewQueries(QueryConfig{Repository: &queryRepository{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := absent.ReadAudit(context.Background(), 0, 10); err == nil {
		t.Error("ReadAudit() succeeded with no trail configured")
	}
}

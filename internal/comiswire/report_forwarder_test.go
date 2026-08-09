package comiswire

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type reportForwarderOutbox struct {
	mu          sync.Mutex
	delivery    application.ComisReportDelivery
	found       bool
	nextErr     error
	markErr     error
	marks       []application.ComisReportAcknowledgement
	deliveredAt []time.Time
	afterMark   func()
}

func (outbox *reportForwarderOutbox) NextComisReport(context.Context) (application.ComisReportDelivery, bool, error) {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	return outbox.delivery, outbox.found, outbox.nextErr
}

func (outbox *reportForwarderOutbox) MarkComisReportDelivered(
	_ context.Context,
	operationID string,
	ack application.ComisReportAcknowledgement,
	deliveredAt time.Time,
) error {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if operationID != outbox.delivery.OperationID {
		return errors.New("operation identity differs")
	}
	if outbox.markErr != nil {
		return outbox.markErr
	}
	outbox.marks = append(outbox.marks, ack)
	outbox.deliveredAt = append(outbox.deliveredAt, deliveredAt)
	outbox.found = false
	if outbox.afterMark != nil {
		outbox.afterMark()
	}
	return nil
}

type reportForwarderSender struct {
	mu       sync.Mutex
	requests []ReportRequestParams
	send     func(int, ReportRequestParams) (ReportResponseResult, error)
}

func (sender *reportForwarderSender) Report(_ context.Context, request ReportRequestParams) (ReportResponseResult, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.requests = append(sender.requests, request)
	return sender.send(len(sender.requests), request)
}

func TestReportForwarder_RetriesExactIdentityAfterUncertainOutcome(t *testing.T) {
	delivery := validForwarderDelivery()
	outbox := &reportForwarderOutbox{delivery: delivery, found: true}
	sender := &reportForwarderSender{send: func(call int, request ReportRequestParams) (ReportResponseResult, error) {
		if call == 1 {
			return ReportResponseResult{}, errors.New("connection lost after write")
		}
		return validForwarderAcknowledgement(request), nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	outbox.afterMark = cancel
	forwarder, err := NewReportForwarder(ReportForwarderConfig{
		Outbox: outbox, Sender: sender, Clock: fixedForwarderClock,
		PollInterval: time.Millisecond, MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewReportForwarder() error = %v", err)
	}
	if err := forwarder.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want cancellation after durable mark", err)
	}
	sender.mu.Lock()
	requests := append([]ReportRequestParams(nil), sender.requests...)
	sender.mu.Unlock()
	if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("requests = %#v, want two exact-identity attempts", requests)
	}
	assertForwarderRequest(t, requests[0], delivery)
	if len(outbox.marks) != 1 || outbox.marks[0].AcceptedSequence != 17 ||
		outbox.marks[0].ManagedRunID != delivery.ManagedRunID ||
		outbox.marks[0].ServiceReportID != delivery.ServiceReportID {
		t.Fatalf("durable marks = %#v", outbox.marks)
	}
	if len(outbox.deliveredAt) != 1 || outbox.deliveredAt[0] != fixedForwarderClock() {
		t.Fatalf("delivered times = %#v", outbox.deliveredAt)
	}
}

func TestReportForwarder_RestartResendsAfterAcknowledgementBeforeMarkCrash(t *testing.T) {
	delivery := validForwarderDelivery()
	crash := errors.New("simulated local store loss")
	outbox := &reportForwarderOutbox{delivery: delivery, found: true, markErr: crash}
	firstSender := &reportForwarderSender{send: func(_ int, request ReportRequestParams) (ReportResponseResult, error) {
		return validForwarderAcknowledgement(request), nil
	}}
	first := newTestReportForwarder(t, outbox, firstSender)
	if err := first.Run(context.Background()); !errors.Is(err, crash) {
		t.Fatalf("first Run() error = %v, want mark failure", err)
	}
	if !outbox.found || len(outbox.marks) != 0 {
		t.Fatalf("outbox after failed mark = found %t, marks %#v", outbox.found, outbox.marks)
	}

	outbox.markErr = nil
	ctx, cancel := context.WithCancel(context.Background())
	outbox.afterMark = cancel
	secondSender := &reportForwarderSender{send: func(_ int, request ReportRequestParams) (ReportResponseResult, error) {
		return validForwarderAcknowledgement(request), nil
	}}
	second := newTestReportForwarder(t, outbox, secondSender)
	if err := second.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Run() error = %v", err)
	}
	if len(firstSender.requests) != 1 || len(secondSender.requests) != 1 ||
		!reflect.DeepEqual(firstSender.requests[0], secondSender.requests[0]) {
		t.Fatalf("restart requests = %#v / %#v, want exact resend", firstSender.requests, secondSender.requests)
	}
}

func TestReportForwarder_MapsClosedSparseVocabulary(t *testing.T) {
	tests := []struct {
		domainKind domain.WorkerReportKind
		wireKind   ReportKind
		keyed      bool
	}{
		{domain.ReportProgress, ReportKindProgress, false},
		{domain.ReportDecision, ReportKindAttention, true},
		{domain.ReportBlocked, ReportKindBlocked, false},
		{domain.ReportPaused, ReportKindPaused, false},
		{domain.ReportCandidateComplete, ReportKindCandidateComplete, false},
		{domain.ReportFailed, ReportKindFailed, false},
		{domain.ReportResolution, ReportKindResolution, true},
	}
	for _, test := range tests {
		delivery := validForwarderDelivery()
		delivery.Kind = test.domainKind
		if test.keyed {
			delivery.ExternalKey = "decision-key-0001"
		} else {
			delivery.ExternalKey = ""
		}
		request, err := reportForwarderRequest(delivery)
		if err != nil {
			t.Fatalf("reportForwarderRequest(%q) error = %v", test.domainKind, err)
		}
		if request.Kind != test.wireKind {
			t.Errorf("kind %q maps to %q, want %q", test.domainKind, request.Kind, test.wireKind)
		}
		if (request.ExternalKey != nil) != test.keyed {
			t.Errorf("kind %q external key = %#v", test.domainKind, request.ExternalKey)
		}
	}
}

func TestReportForwarder_InvalidConfigurationEvidenceAndCancellationFailClosed(t *testing.T) {
	valid := ReportForwarderConfig{
		Outbox: &reportForwarderOutbox{}, Sender: &reportForwarderSender{send: func(_ int, _ ReportRequestParams) (ReportResponseResult, error) {
			return ReportResponseResult{}, nil
		}}, Clock: fixedForwarderClock,
		PollInterval: time.Millisecond, MinimumBackoff: time.Millisecond, MaximumBackoff: time.Second,
	}
	tests := []struct {
		name   string
		mutate func(*ReportForwarderConfig)
	}{
		{name: "outbox", mutate: func(config *ReportForwarderConfig) { config.Outbox = nil }},
		{name: "sender", mutate: func(config *ReportForwarderConfig) { config.Sender = nil }},
		{name: "clock", mutate: func(config *ReportForwarderConfig) { config.Clock = nil }},
		{name: "poll", mutate: func(config *ReportForwarderConfig) { config.PollInterval = 0 }},
		{name: "minimum backoff", mutate: func(config *ReportForwarderConfig) { config.MinimumBackoff = 0 }},
		{name: "maximum backoff", mutate: func(config *ReportForwarderConfig) { config.MaximumBackoff = time.Nanosecond }},
	}
	for _, test := range tests {
		config := valid
		test.mutate(&config)
		if _, err := NewReportForwarder(config); err == nil {
			t.Errorf("NewReportForwarder(%s) error = nil", test.name)
		}
	}
	forwarder, err := NewReportForwarder(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := forwarder.Run(nilForwarderContext()); err == nil {
		t.Fatal("Run(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := forwarder.Run(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v", err)
	}

	invalid := validForwarderDelivery()
	invalid.Kind = "invented"
	if _, err := reportForwarderRequest(invalid); err == nil {
		t.Fatal("reportForwarderRequest(unknown kind) error = nil")
	}
	invalid = validForwarderDelivery()
	preEpoch := time.Date(1960, time.January, 1, 0, 0, 0, 0, time.UTC)
	invalid.WorkerObservedAt = &preEpoch
	if _, err := reportForwarderRequest(invalid); err == nil {
		t.Fatal("reportForwarderRequest(pre-epoch observation) error = nil")
	}
	request, err := reportForwarderRequest(validForwarderDelivery())
	if err != nil {
		t.Fatal(err)
	}
	forged := validForwarderAcknowledgement(request)
	forged.ManagedRunID = "managed-run-forged"
	if _, err := reportForwarderAcknowledgement(validForwarderDelivery(), forged, fixedForwarderClock()); err == nil {
		t.Fatal("reportForwarderAcknowledgement(forged identity) error = nil")
	}
}

func TestReportForwarder_InvalidDurableAndHostEvidenceStopsBeforeMark(t *testing.T) {
	invalidDeliveries := []application.ComisReportDelivery{
		func() application.ComisReportDelivery {
			value := validForwarderDelivery()
			value.TaskHandle = "bad task"
			return value
		}(),
		func() application.ComisReportDelivery {
			value := validForwarderDelivery()
			value.LocalReportID = "bad report"
			return value
		}(),
		func() application.ComisReportDelivery {
			value := validForwarderDelivery()
			value.StateVersion = 0
			return value
		}(),
		func() application.ComisReportDelivery {
			value := validForwarderDelivery()
			value.OperationID = "bad operation"
			return value
		}(),
		func() application.ComisReportDelivery {
			value := validForwarderDelivery()
			value.Summary = string(make([]byte, MaxReportBytes))
			return value
		}(),
	}
	for _, delivery := range invalidDeliveries {
		if _, err := reportForwarderRequest(delivery); err == nil {
			t.Errorf("reportForwarderRequest(%#v) error = nil", delivery)
		}
	}

	delivery := validForwarderDelivery()
	outbox := &reportForwarderOutbox{delivery: delivery, found: true}
	badResult := validForwarderAcknowledgement(ReportRequestParams{
		ManagedRunID: ManagedRunID(delivery.ManagedRunID), ServiceReportID: ServiceReportID(delivery.ServiceReportID),
	})
	badResult.AcceptedSequence = 0
	forwarder := newTestReportForwarder(t, outbox, &reportForwarderSender{send: func(_ int, _ ReportRequestParams) (ReportResponseResult, error) {
		return badResult, nil
	}})
	if err := forwarder.Run(context.Background()); err == nil {
		t.Fatal("Run(invalid acknowledgement) error = nil")
	}
	if len(outbox.marks) != 0 {
		t.Fatalf("marks after invalid acknowledgement = %#v", outbox.marks)
	}

	validResult := validForwarderAcknowledgement(ReportRequestParams{
		ManagedRunID: ManagedRunID(delivery.ManagedRunID), ServiceReportID: ServiceReportID(delivery.ServiceReportID),
	})
	if _, err := reportForwarderAcknowledgement(delivery, validResult, time.Time{}); err == nil {
		t.Fatal("reportForwarderAcknowledgement(zero delivery time) error = nil")
	}
	if _, err := reportForwarderAcknowledgement(delivery, validResult, fixedForwarderClock().In(time.FixedZone("other", 3600))); err == nil {
		t.Fatal("reportForwarderAcknowledgement(non-UTC delivery time) error = nil")
	}
	validResult.RetainedUntilMs = fixedForwarderClock().UnixMilli()
	if _, err := reportForwarderAcknowledgement(delivery, validResult, fixedForwarderClock()); err == nil {
		t.Fatal("reportForwarderAcknowledgement(expired retention) error = nil")
	}
}

func TestReportForwarder_CancellationDuringUncertainSendAndBackoffBounds(t *testing.T) {
	delivery := validForwarderDelivery()
	outbox := &reportForwarderOutbox{delivery: delivery, found: true}
	ctx, cancel := context.WithCancel(context.Background())
	forwarder := newTestReportForwarder(t, outbox, &reportForwarderSender{send: func(_ int, _ ReportRequestParams) (ReportResponseResult, error) {
		cancel()
		return ReportResponseResult{}, errors.New("uncertain cancellation")
	}})
	if err := forwarder.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled send) error = %v", err)
	}
	if next := nextReportBackoff(time.Millisecond, 8*time.Millisecond); next != 2*time.Millisecond {
		t.Fatalf("nextReportBackoff(minimum) = %v", next)
	}
	if next := nextReportBackoff(5*time.Millisecond, 8*time.Millisecond); next != 8*time.Millisecond {
		t.Fatalf("nextReportBackoff(near maximum) = %v", next)
	}
	if next := nextReportBackoff(8*time.Millisecond, 8*time.Millisecond); next != 8*time.Millisecond {
		t.Fatalf("nextReportBackoff(maximum) = %v", next)
	}
}

func TestReportForwarder_StoreFailuresAndEmptyPollingAreJoined(t *testing.T) {
	readFailure := errors.New("read failed")
	outbox := &reportForwarderOutbox{nextErr: readFailure}
	forwarder := newTestReportForwarder(t, outbox, &reportForwarderSender{send: func(_ int, _ ReportRequestParams) (ReportResponseResult, error) {
		return ReportResponseResult{}, nil
	}})
	if err := forwarder.Run(context.Background()); !errors.Is(err, readFailure) {
		t.Fatalf("Run(read failure) error = %v", err)
	}

	empty := &reportForwarderOutbox{}
	idle := newTestReportForwarder(t, empty, &reportForwarderSender{send: func(_ int, _ ReportRequestParams) (ReportResponseResult, error) {
		return ReportResponseResult{}, errors.New("must not send")
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- idle.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run(empty cancellation) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("empty forwarder did not join cancellation")
	}
}

func newTestReportForwarder(t *testing.T, outbox application.ComisReportOutbox, sender ReportSender) *ReportForwarder {
	t.Helper()
	forwarder, err := NewReportForwarder(ReportForwarderConfig{
		Outbox: outbox, Sender: sender, Clock: fixedForwarderClock,
		PollInterval: time.Millisecond, MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return forwarder
}

func validForwarderDelivery() application.ComisReportDelivery {
	observed := time.Date(2026, time.August, 9, 18, 0, 0, 123_000_000, time.UTC)
	return application.ComisReportDelivery{
		OperationID: "report-operation-0001", TaskHandle: "task-0001", LocalReportID: "local-report-0001",
		ManagedRunID: "managed-run-0001", ServiceReportID: "service-report-0001",
		Kind: domain.ReportDecision, ExternalKey: "decision-key-0001", Summary: "Choose a bounded option",
		Details: "The worker needs one external decision.", WorkerObservedAt: &observed, StateVersion: 7,
	}
}

func validForwarderAcknowledgement(request ReportRequestParams) ReportResponseResult {
	return ReportResponseResult{
		AcceptedSequence: 17, ManagedRunID: request.ManagedRunID,
		ServiceReportID: request.ServiceReportID, RetainedUntilMs: fixedForwarderClock().Add(24 * time.Hour).UnixMilli(),
	}
}

func fixedForwarderClock() time.Time {
	return time.Date(2026, time.August, 9, 18, 5, 0, 0, time.UTC)
}

func nilForwarderContext() context.Context { return nil }

func assertForwarderRequest(t *testing.T, request ReportRequestParams, delivery application.ComisReportDelivery) {
	t.Helper()
	if string(request.OperationID) != delivery.OperationID || string(request.ManagedRunID) != delivery.ManagedRunID ||
		string(request.ServiceReportID) != delivery.ServiceReportID || request.Kind != ReportKindAttention ||
		request.Summary != delivery.Summary || request.Details == nil || *request.Details != delivery.Details ||
		request.ExternalKey == nil || *request.ExternalKey != delivery.ExternalKey || request.ObservedAtMs == nil ||
		*request.ObservedAtMs != delivery.WorkerObservedAt.UnixMilli() || len(request.ArtifactRefs) != 0 {
		t.Fatalf("request = %#v, want exact mapped delivery", request)
	}
}

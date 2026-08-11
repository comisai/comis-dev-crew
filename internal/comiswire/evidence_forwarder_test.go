package comiswire

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type evidenceForwarderOutbox struct {
	delivery     application.ComisEvidenceDelivery
	marked       application.ComisEvidenceAcknowledgement
	cancel       context.CancelFunc
	empty        bool
	cancelOnNext bool
	nextErr      error
	markErr      error
}

func (outbox *evidenceForwarderOutbox) NextComisEvidence(context.Context) (application.ComisEvidenceDelivery, bool, error) {
	if outbox.cancelOnNext && outbox.cancel != nil {
		outbox.cancel()
	}
	return outbox.delivery, !outbox.empty, outbox.nextErr
}

func (outbox *evidenceForwarderOutbox) MarkComisEvidenceDelivered(
	_ context.Context,
	_ string,
	ack application.ComisEvidenceAcknowledgement,
	_ time.Time,
) error {
	if outbox.markErr != nil {
		return outbox.markErr
	}
	outbox.marked = ack
	if outbox.cancel != nil {
		outbox.cancel()
	}
	return nil
}

type evidenceForwarderSender struct {
	request         PutEvidenceRequestParams
	result          PutEvidenceResponseResult
	err             error
	errorsRemaining int
}

func (sender *evidenceForwarderSender) PutEvidence(
	_ context.Context,
	request PutEvidenceRequestParams,
) (PutEvidenceResponseResult, error) {
	sender.request = request
	if sender.errorsRemaining > 0 {
		sender.errorsRemaining--
		return PutEvidenceResponseResult{}, sender.err
	}
	return sender.result, nil
}

func TestEvidenceForwarderPublishesExactDurableAttachmentBeforeAcknowledging(t *testing.T) {
	now := time.Date(2026, time.August, 11, 21, 0, 0, 0, time.UTC)
	expiresAt := now.Add(10 * time.Minute)
	retainedUntilMs := now.Add(24 * time.Hour).UnixMilli()
	body := []byte("exact scout report")
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	ctx, cancel := context.WithCancel(context.Background())
	outbox := &evidenceForwarderOutbox{cancel: cancel, delivery: application.ComisEvidenceDelivery{
		ComisEvidencePublication: application.ComisEvidencePublication{
			OperationID: "put-evidence-report", TaskHandle: "task-report",
			EvidenceRef: "evidence-report", Kind: "report_artifact", SubjectDigest: "b" + contentHash[1:],
			ObservedAt: now, ExpiresAt: &expiresAt, ContentHash: contentHash,
			VerificationLevel: application.ComisEvidenceAdapterVerified, Body: body,
			Delivery: &application.ComisEvidenceDeliveryRequest{
				Kind: application.ComisEvidenceAttachment, FileName: "report.md", MediaType: "text/markdown",
			},
		},
		ManagedRunID: "managed-run-report", StateVersion: 7,
	}}
	sender := &evidenceForwarderSender{result: PutEvidenceResponseResult{
		ManagedRunID: "managed-run-report", EvidenceRef: "evidence-report", ContentHash: contentHash,
		VerificationLevel: EvidenceVerificationLevelAdapterVerified, RetainedUntilMs: &retainedUntilMs,
	}}
	forwarder, err := NewEvidenceForwarder(EvidenceForwarderConfig{
		Outbox: outbox, Sender: sender, Clock: func() time.Time { return now },
		PollInterval: time.Millisecond, MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewEvidenceForwarder() error = %v", err)
	}
	if err := forwarder.Run(ctx); err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
	if sender.request.OperationID != "put-evidence-report" || sender.request.ContentHash != contentHash ||
		sender.request.BodyBase64 != "ZXhhY3Qgc2NvdXQgcmVwb3J0" || sender.request.Delivery == nil ||
		sender.request.Delivery.Kind != EvidenceDeliveryKindAttachment ||
		sender.request.Delivery.FileName == nil || *sender.request.Delivery.FileName != "report.md" {
		t.Fatalf("PutEvidence() request = %#v", sender.request)
	}
	if outbox.marked.EvidenceRef != "evidence-report" || outbox.marked.RetainedUntil == nil ||
		outbox.marked.VerificationLevel != application.ComisEvidenceAdapterVerified {
		t.Fatalf("durable acknowledgement = %#v", outbox.marked)
	}
}

func TestEvidenceForwarderRejectsInvalidDurableEvidenceAndAcknowledgements(t *testing.T) {
	now := time.Date(2026, time.August, 11, 21, 0, 0, 0, time.UTC)
	delivery := validEvidenceForwarderDelivery(now)
	if _, err := evidenceForwarderRequest(application.ComisEvidenceDelivery{}); err == nil {
		t.Fatal("evidenceForwarderRequest(invalid delivery) error = nil")
	}
	invalidVerification := delivery
	invalidVerification.VerificationLevel = "invented"
	if _, err := evidenceForwarderRequest(invalidVerification); err == nil {
		t.Fatal("evidenceForwarderRequest(invalid verification) error = nil")
	}
	negativeTime := delivery
	negativeTime.ObservedAt = time.Date(1960, time.January, 1, 0, 0, 0, 0, time.UTC)
	if _, err := evidenceForwarderRequest(negativeTime); err == nil {
		t.Fatal("evidenceForwarderRequest(negative time) error = nil")
	}
	invalidExpiry := delivery
	invalidExpiry.ExpiresAt = &invalidExpiry.ObservedAt
	if _, err := evidenceForwarderRequest(invalidExpiry); err == nil {
		t.Fatal("evidenceForwarderRequest(invalid expiry) error = nil")
	}
	badReference := delivery
	badReference.Delivery = &application.ComisEvidenceDeliveryRequest{
		Kind: application.ComisEvidenceReference, FileName: "unexpected.txt",
	}
	if _, err := evidenceForwarderRequest(badReference); err == nil {
		t.Fatal("evidenceForwarderRequest(bad reference metadata) error = nil")
	}
	badAttachment := delivery
	badAttachment.Delivery = &application.ComisEvidenceDeliveryRequest{Kind: application.ComisEvidenceAttachment}
	if _, err := evidenceForwarderRequest(badAttachment); err == nil {
		t.Fatal("evidenceForwarderRequest(bad attachment metadata) error = nil")
	}
	badKind := delivery
	badKind.Delivery = &application.ComisEvidenceDeliveryRequest{Kind: "invented"}
	if _, err := evidenceForwarderRequest(badKind); err == nil {
		t.Fatal("evidenceForwarderRequest(bad delivery kind) error = nil")
	}
	badDocument := delivery
	badDocument.SubjectDigest = "not-a-digest"
	if _, err := evidenceForwarderRequest(badDocument); err == nil {
		t.Fatal("evidenceForwarderRequest(invalid wire document) error = nil")
	}
	reference := delivery
	reference.VerificationLevel = application.ComisEvidenceReported
	reference.Delivery = &application.ComisEvidenceDeliveryRequest{Kind: application.ComisEvidenceReference}
	request, err := evidenceForwarderRequest(reference)
	if err != nil || request.Delivery == nil || request.Delivery.Kind != EvidenceDeliveryKindReference ||
		request.VerificationLevel != EvidenceVerificationLevelReported {
		t.Fatalf("evidenceForwarderRequest(reference) = %#v, %v", request, err)
	}
	if _, err := evidenceForwarderAcknowledgement(delivery, PutEvidenceResponseResult{}, now); err == nil {
		t.Fatal("evidenceForwarderAcknowledgement(invalid document) error = nil")
	}
	result := evidenceForwarderResult(delivery)
	result.ManagedRunID = "managed-run-other"
	if _, err := evidenceForwarderAcknowledgement(delivery, result, now); err == nil {
		t.Fatal("evidenceForwarderAcknowledgement(identity mismatch) error = nil")
	}
	result = evidenceForwarderResult(delivery)
	retainedUntil := now.UnixMilli()
	result.RetainedUntilMs = &retainedUntil
	if _, err := evidenceForwarderAcknowledgement(delivery, result, now); err == nil {
		t.Fatal("evidenceForwarderAcknowledgement(invalid retention) error = nil")
	}
}

func TestEvidenceForwarderRunFailsClosedAndRetriesUncertainSend(t *testing.T) {
	if _, err := NewEvidenceForwarder(EvidenceForwarderConfig{}); err == nil {
		t.Fatal("NewEvidenceForwarder(invalid) error = nil")
	}
	if err := (*EvidenceForwarder)(nil).Run(context.Background()); err == nil {
		t.Fatal("Run(nil forwarder) error = nil")
	}
	now := time.Date(2026, time.August, 11, 21, 0, 0, 0, time.UTC)
	delivery := validEvidenceForwarderDelivery(now)
	newForwarder := func(outbox *evidenceForwarderOutbox, sender *evidenceForwarderSender, clock application.Clock) *EvidenceForwarder {
		forwarder, err := NewEvidenceForwarder(EvidenceForwarderConfig{
			Outbox: outbox, Sender: sender, Clock: clock, PollInterval: time.Millisecond,
			MinimumBackoff: time.Millisecond, MaximumBackoff: 2 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewEvidenceForwarder() error = %v", err)
		}
		return forwarder
	}
	canceled, cancelCanceled := context.WithCancel(context.Background())
	cancelCanceled()
	if err := newForwarder(&evidenceForwarderOutbox{}, &evidenceForwarderSender{}, func() time.Time { return now }).Run(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(canceled) error = %v, want context.Canceled", err)
	}
	readFailure := &evidenceForwarderOutbox{nextErr: errors.New("outbox unavailable")}
	if err := newForwarder(readFailure, &evidenceForwarderSender{}, func() time.Time { return now }).Run(context.Background()); err == nil {
		t.Fatal("Run(outbox failure) error = nil")
	}
	emptyContext, cancelEmpty := context.WithCancel(context.Background())
	empty := &evidenceForwarderOutbox{empty: true, cancelOnNext: true, cancel: cancelEmpty}
	if err := newForwarder(empty, &evidenceForwarderSender{}, func() time.Time { return now }).Run(emptyContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(empty cancellation) error = %v, want context.Canceled", err)
	}
	invalid := &evidenceForwarderOutbox{delivery: application.ComisEvidenceDelivery{}}
	if err := newForwarder(invalid, &evidenceForwarderSender{}, func() time.Time { return now }).Run(context.Background()); err == nil {
		t.Fatal("Run(invalid durable evidence) error = nil")
	}
	retryContext, cancelRetry := context.WithCancel(context.Background())
	retryOutbox := &evidenceForwarderOutbox{delivery: delivery, cancel: cancelRetry}
	retrySender := &evidenceForwarderSender{
		result: evidenceForwarderResult(delivery), err: errors.New("uncertain send"), errorsRemaining: 1,
	}
	if err := newForwarder(retryOutbox, retrySender, func() time.Time { return now }).Run(retryContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(retry) error = %v, want context.Canceled", err)
	}
	invalidClock := &evidenceForwarderOutbox{delivery: delivery}
	if err := newForwarder(invalidClock, &evidenceForwarderSender{result: evidenceForwarderResult(delivery)}, func() time.Time { return time.Time{} }).Run(context.Background()); err == nil {
		t.Fatal("Run(invalid clock) error = nil")
	}
	markFailure := &evidenceForwarderOutbox{delivery: delivery, markErr: errors.New("outbox write unavailable")}
	if err := newForwarder(markFailure, &evidenceForwarderSender{result: evidenceForwarderResult(delivery)}, func() time.Time { return now }).Run(context.Background()); err == nil {
		t.Fatal("Run(mark failure) error = nil")
	}
}

func validEvidenceForwarderDelivery(now time.Time) application.ComisEvidenceDelivery {
	body := []byte("durable evidence")
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	expiresAt := now.Add(10 * time.Minute)
	return application.ComisEvidenceDelivery{
		ComisEvidencePublication: application.ComisEvidencePublication{
			OperationID: "put-evidence-valid", TaskHandle: "task-evidence",
			EvidenceRef: "evidence-valid", Kind: "candidate_bundle", SubjectDigest: contentHash,
			ObservedAt: now, ExpiresAt: &expiresAt, ContentHash: contentHash,
			VerificationLevel: application.ComisEvidenceAdapterVerified, Body: body,
		},
		ManagedRunID: "managed-run-evidence", StateVersion: 7,
	}
}

func evidenceForwarderResult(delivery application.ComisEvidenceDelivery) PutEvidenceResponseResult {
	return PutEvidenceResponseResult{
		ManagedRunID: ManagedRunID(delivery.ManagedRunID), EvidenceRef: EvidenceRef(delivery.EvidenceRef),
		ContentHash: delivery.ContentHash, VerificationLevel: EvidenceVerificationLevelAdapterVerified,
	}
}

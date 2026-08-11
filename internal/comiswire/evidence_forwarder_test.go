package comiswire

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type evidenceForwarderOutbox struct {
	delivery application.ComisEvidenceDelivery
	marked   application.ComisEvidenceAcknowledgement
	cancel   context.CancelFunc
}

func (outbox *evidenceForwarderOutbox) NextComisEvidence(context.Context) (application.ComisEvidenceDelivery, bool, error) {
	return outbox.delivery, true, nil
}

func (outbox *evidenceForwarderOutbox) MarkComisEvidenceDelivered(
	_ context.Context,
	_ string,
	ack application.ComisEvidenceAcknowledgement,
	_ time.Time,
) error {
	outbox.marked = ack
	outbox.cancel()
	return nil
}

type evidenceForwarderSender struct {
	request PutEvidenceRequestParams
	result  PutEvidenceResponseResult
}

func (sender *evidenceForwarderSender) PutEvidence(
	_ context.Context,
	request PutEvidenceRequestParams,
) (PutEvidenceResponseResult, error) {
	sender.request = request
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

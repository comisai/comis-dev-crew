package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const comisEvidenceOutboxMigration = `
CREATE TABLE comis_evidence_outbox (
    operation_id TEXT PRIMARY KEY,
    task_handle TEXT NOT NULL,
    evidence_ref TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL,
    subject_digest TEXT NOT NULL,
    observed_at TEXT NOT NULL,
    expires_at TEXT,
    content_hash TEXT NOT NULL,
    verification_level TEXT NOT NULL,
    body BLOB NOT NULL,
    delivery_kind TEXT NOT NULL,
    file_name TEXT NOT NULL,
    media_type TEXT NOT NULL,
    retained_until TEXT,
    delivered_at TEXT,
    state_version INTEGER NOT NULL,
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
CREATE INDEX comis_evidence_outbox_pending_idx
ON comis_evidence_outbox(delivered_at, state_version, evidence_ref);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (14, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

var evidenceMediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,63}/[a-z0-9][a-z0-9.+-]{0,63}$`)

func insertCandidatePublications(
	ctx context.Context,
	target execer,
	task domain.Task,
	evidence *domain.SealedDeliveryEvidence,
	publications []application.ComisEvidencePublication,
	stateVersion int64,
) error {
	if err := validateCandidatePublications(task, evidence, publications); err != nil {
		return err
	}
	const insert = `INSERT INTO comis_evidence_outbox (
        operation_id, task_handle, evidence_ref, kind, subject_digest,
        observed_at, expires_at, content_hash, verification_level, body,
        delivery_kind, file_name, media_type, state_version
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, publication := range publications {
		var expiresAt any
		if publication.ExpiresAt != nil {
			expiresAt = formatTime(*publication.ExpiresAt)
		}
		deliveryKind, fileName, mediaType := publicationDeliveryColumns(publication.Delivery)
		if _, err := target.ExecContext(ctx, insert,
			publication.OperationID, publication.TaskHandle, publication.EvidenceRef,
			publication.Kind, publication.SubjectDigest, formatTime(publication.ObservedAt),
			expiresAt, publication.ContentHash, publication.VerificationLevel, publication.Body,
			deliveryKind, fileName, mediaType, stateVersion,
		); isConstraintError(err) {
			return fmt.Errorf("insert Comis evidence publication: %w", application.ErrConflict)
		} else if err != nil {
			return fmt.Errorf("insert Comis evidence publication: %w", err)
		}
	}
	return nil
}

func publicationDeliveryColumns(delivery *application.ComisEvidenceDeliveryRequest) (string, string, string) {
	if delivery == nil {
		return "", "", ""
	}
	return string(delivery.Kind), delivery.FileName, delivery.MediaType
}

func candidatePublicationsMatch(
	ctx context.Context,
	source queryer,
	publications []application.ComisEvidencePublication,
) (bool, error) {
	const query = `SELECT
        task_handle, evidence_ref, kind, subject_digest, observed_at, expires_at,
        content_hash, verification_level, body, delivery_kind, file_name, media_type
    FROM comis_evidence_outbox WHERE operation_id = ?`
	for _, publication := range publications {
		var taskHandle, evidenceRef, kind, subjectDigest, observedAt, contentHash string
		var expiresAt sql.NullString
		var verification application.ComisEvidenceVerification
		var body []byte
		var deliveryKind, fileName, mediaType string
		if err := source.QueryRowContext(ctx, query, publication.OperationID).Scan(
			&taskHandle, &evidenceRef, &kind, &subjectDigest, &observedAt, &expiresAt,
			&contentHash, &verification, &body, &deliveryKind, &fileName, &mediaType,
		); errors.Is(err, sql.ErrNoRows) {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("read candidate publication replay: %w", err)
		}
		wantExpiresAt := ""
		if publication.ExpiresAt != nil {
			wantExpiresAt = formatTime(*publication.ExpiresAt)
		}
		wantDeliveryKind, wantFileName, wantMediaType := publicationDeliveryColumns(publication.Delivery)
		if taskHandle != publication.TaskHandle || evidenceRef != publication.EvidenceRef ||
			kind != publication.Kind || subjectDigest != publication.SubjectDigest ||
			observedAt != formatTime(publication.ObservedAt) || expiresAt.String != wantExpiresAt ||
			contentHash != publication.ContentHash || verification != publication.VerificationLevel ||
			!bytes.Equal(body, publication.Body) || deliveryKind != wantDeliveryKind ||
			fileName != wantFileName || mediaType != wantMediaType {
			return false, nil
		}
	}
	return true, nil
}

func validateCandidatePublications(
	task domain.Task,
	evidence *domain.SealedDeliveryEvidence,
	publications []application.ComisEvidencePublication,
) error {
	if evidence == nil || len(publications) != 2 || task.ManagedRunID == "" {
		return errors.New("commit candidate evidence: two publications are required")
	}
	bundle := evidence.Bundle()
	seenOperations := make(map[string]struct{}, len(publications))
	seenReferences := make(map[string]struct{}, len(publications))
	for _, publication := range publications {
		if domain.ValidateOperationID(publication.OperationID) != nil ||
			domain.ValidateAuthorityReference("evidenceRef", publication.EvidenceRef) != nil ||
			domain.ValidateAuthorityReference("evidenceKind", publication.Kind) != nil ||
			publication.TaskHandle != task.Handle || publication.SubjectDigest != evidence.Digest() ||
			publication.ObservedAt.Location() != time.UTC || !publication.ObservedAt.Equal(bundle.ProducedAt) ||
			publication.ExpiresAt == nil || publication.ExpiresAt.Location() != time.UTC ||
			!publication.ExpiresAt.Equal(bundle.ExpiresAt) || len(publication.Body) == 0 ||
			len(publication.Body) > 1<<20 || publication.VerificationLevel != application.ComisEvidenceAdapterVerified ||
			fmt.Sprintf("%x", sha256.Sum256(publication.Body)) != publication.ContentHash {
			return errors.New("commit candidate evidence: publication is invalid")
		}
		if _, found := seenOperations[publication.OperationID]; found {
			return errors.New("commit candidate evidence: publication operation is duplicated")
		}
		if _, found := seenReferences[publication.EvidenceRef]; found {
			return errors.New("commit candidate evidence: publication reference is duplicated")
		}
		seenOperations[publication.OperationID] = struct{}{}
		seenReferences[publication.EvidenceRef] = struct{}{}
	}
	outcome := publications[0]
	if outcome.Kind != "candidate_bundle" || outcome.Delivery != nil ||
		outcome.ContentHash != evidence.Digest() || !bytes.Equal(outcome.Body, evidence.Canonical()) {
		return errors.New("commit candidate evidence: outcome publication differs")
	}
	delivery := publications[1]
	switch {
	case bundle.ForgeEvidence != nil:
		if delivery.Kind != "delivery_reference" || delivery.Delivery == nil ||
			delivery.Delivery.Kind != application.ComisEvidenceReference ||
			delivery.Delivery.FileName != "" || delivery.Delivery.MediaType != "" ||
			!validEvidenceReference(string(delivery.Body)) {
			return errors.New("commit candidate evidence: reference publication differs")
		}
	case bundle.ReportArtifact != nil:
		artifact := bundle.ReportArtifact
		if delivery.Kind != "report_artifact" || delivery.Delivery == nil ||
			delivery.Delivery.Kind != application.ComisEvidenceAttachment ||
			delivery.ContentHash != artifact.ContentHash || int64(len(delivery.Body)) != artifact.Size ||
			strings.ContainsAny(delivery.Delivery.FileName, "/\\\x00\r\n") ||
			filepath.Base(delivery.Delivery.FileName) != delivery.Delivery.FileName ||
			!evidenceMediaTypePattern.MatchString(delivery.Delivery.MediaType) ||
			delivery.Delivery.MediaType != artifact.MediaType {
			return errors.New("commit candidate evidence: attachment publication differs")
		}
	default:
		return errors.New("commit candidate evidence: delivery authority is unavailable")
	}
	return nil
}

func validEvidenceReference(value string) bool {
	reference, err := url.Parse(value)
	return err == nil && reference.Scheme == "https" && reference.Host != "" &&
		reference.User == nil && reference.Fragment == "" && reference.RawQuery == ""
}

// NextComisEvidence returns the oldest immutable publication without a durable
// host acknowledgement. The same operation identity remains eligible on retry.
func (store *Store) NextComisEvidence(ctx context.Context) (application.ComisEvidenceDelivery, bool, error) {
	const query = `SELECT
        o.operation_id, o.task_handle, o.evidence_ref, o.kind, o.subject_digest,
        o.observed_at, o.expires_at, o.content_hash, o.verification_level, o.body,
        o.delivery_kind, o.file_name, o.media_type, o.state_version, t.managed_run_id
    FROM comis_evidence_outbox o JOIN tasks t ON t.handle = o.task_handle
    WHERE o.delivered_at IS NULL
    ORDER BY o.state_version, o.evidence_ref
    LIMIT 1`
	var result application.ComisEvidenceDelivery
	var observedAt string
	var expiresAt sql.NullString
	var deliveryKind string
	var fileName string
	var mediaType string
	err := store.db.QueryRowContext(ctx, query).Scan(
		&result.OperationID, &result.TaskHandle, &result.EvidenceRef, &result.Kind, &result.SubjectDigest,
		&observedAt, &expiresAt, &result.ContentHash, &result.VerificationLevel, &result.Body,
		&deliveryKind, &fileName, &mediaType, &result.StateVersion, &result.ManagedRunID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return application.ComisEvidenceDelivery{}, false, nil
	}
	if err != nil {
		return application.ComisEvidenceDelivery{}, false, fmt.Errorf("read Comis evidence outbox: %w", err)
	}
	result.ObservedAt, err = parseTime(observedAt)
	if err != nil {
		return application.ComisEvidenceDelivery{}, false, errors.New("read Comis evidence outbox: observation time is invalid")
	}
	if expiresAt.Valid {
		parsed, parseErr := parseTime(expiresAt.String)
		if parseErr != nil {
			return application.ComisEvidenceDelivery{}, false, errors.New("read Comis evidence outbox: expiry time is invalid")
		}
		result.ExpiresAt = &parsed
	}
	if deliveryKind != "" {
		result.Delivery = &application.ComisEvidenceDeliveryRequest{
			Kind: application.ComisEvidenceDeliveryKind(deliveryKind), FileName: fileName, MediaType: mediaType,
		}
	}
	if err := validateStoredEvidenceDelivery(result); err != nil {
		return application.ComisEvidenceDelivery{}, false, err
	}
	return result, true, nil
}

func validateStoredEvidenceDelivery(delivery application.ComisEvidenceDelivery) error {
	if domain.ValidateAuthorityReference("managedRunId", delivery.ManagedRunID) != nil || delivery.StateVersion < 1 ||
		domain.ValidateOperationID(delivery.OperationID) != nil ||
		domain.ValidateAuthorityReference("evidenceRef", delivery.EvidenceRef) != nil ||
		domain.ValidateAuthorityReference("evidenceKind", delivery.Kind) != nil ||
		domain.ValidateTaskHandle(delivery.TaskHandle) != nil || delivery.ObservedAt.Location() != time.UTC ||
		len(delivery.Body) == 0 || len(delivery.Body) > 1<<20 ||
		fmt.Sprintf("%x", sha256.Sum256(delivery.Body)) != delivery.ContentHash ||
		delivery.VerificationLevel != application.ComisEvidenceAdapterVerified {
		return errors.New("read Comis evidence outbox: durable publication is invalid")
	}
	return nil
}

// MarkComisEvidenceDelivered records only an exact host acknowledgement and
// treats a byte-identical repeated acknowledgement as success.
func (store *Store) MarkComisEvidenceDelivered(
	ctx context.Context,
	operationID string,
	ack application.ComisEvidenceAcknowledgement,
	deliveredAt time.Time,
) error {
	if domain.ValidateOperationID(operationID) != nil ||
		domain.ValidateAuthorityReference("managedRunId", ack.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("evidenceRef", ack.EvidenceRef) != nil ||
		deliveredAt.IsZero() || deliveredAt.Location() != time.UTC ||
		ack.VerificationLevel != application.ComisEvidenceAdapterVerified {
		return errors.New("mark Comis evidence delivered: acknowledgement is invalid")
	}
	if ack.RetainedUntil != nil && (ack.RetainedUntil.Location() != time.UTC || !ack.RetainedUntil.After(deliveredAt)) {
		return errors.New("mark Comis evidence delivered: retention is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Comis evidence acknowledgement: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	const query = `SELECT
        t.managed_run_id, o.evidence_ref, o.content_hash, o.verification_level,
        o.retained_until, o.delivered_at
    FROM comis_evidence_outbox o JOIN tasks t ON t.handle = o.task_handle
    WHERE o.operation_id = ?`
	var managedRunID, evidenceRef, contentHash string
	var verification application.ComisEvidenceVerification
	var retainedUntil, priorDeliveredAt sql.NullString
	if err := transaction.QueryRowContext(ctx, query, operationID).Scan(
		&managedRunID, &evidenceRef, &contentHash, &verification, &retainedUntil, &priorDeliveredAt,
	); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("mark Comis evidence delivered: %w", application.ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("read Comis evidence acknowledgement: %w", err)
	}
	if managedRunID != ack.ManagedRunID || evidenceRef != ack.EvidenceRef ||
		contentHash != ack.ContentHash || verification != ack.VerificationLevel {
		return fmt.Errorf("evidence acknowledgement identity: %w", application.ErrConflict)
	}
	wantRetention := ""
	if ack.RetainedUntil != nil {
		wantRetention = formatTime(*ack.RetainedUntil)
	}
	if priorDeliveredAt.Valid {
		if retainedUntil.String != wantRetention {
			return fmt.Errorf("evidence acknowledgement replay: %w", application.ErrConflict)
		}
		return nil
	}
	const update = `UPDATE comis_evidence_outbox SET retained_until = ?, delivered_at = ?
        WHERE operation_id = ? AND delivered_at IS NULL`
	var retention any
	if ack.RetainedUntil != nil {
		retention = wantRetention
	}
	result, err := transaction.ExecContext(ctx, update, retention, formatTime(deliveredAt), operationID)
	if err != nil {
		return fmt.Errorf("write Comis evidence acknowledgement: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("write Comis evidence acknowledgement: exact item was not updated")
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit Comis evidence acknowledgement: %w", err)
	}
	return nil
}

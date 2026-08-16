package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var mediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,63}/[a-z0-9][a-z0-9.+-]{0,63}$`)

// WorktreeCleanliness is the closed Git posture captured with candidate evidence.
type WorktreeCleanliness string

const (
	WorktreeClean   WorktreeCleanliness = "clean"
	WorktreeDirty   WorktreeCleanliness = "dirty"
	WorktreeUnknown WorktreeCleanliness = "unknown"
)

// CheckConclusion is the closed local and forge check outcome vocabulary.
type CheckConclusion string

const (
	CheckPassed  CheckConclusion = "passed"
	CheckFailed  CheckConclusion = "failed"
	CheckPending CheckConclusion = "pending"
	CheckUnknown CheckConclusion = "unknown"
)

// ValidationEvidenceReceipt is immutable output from one reviewed local check.
type ValidationEvidenceReceipt struct {
	CheckID      string          `json:"checkId"`
	ProgramID    string          `json:"programId"`
	HeadRevision string          `json:"headRevision"`
	Conclusion   CheckConclusion `json:"conclusion"`
	Required     bool            `json:"required"`
	OutputHash   string          `json:"outputHash"`
	StartedAt    time.Time       `json:"startedAt"`
	CompletedAt  time.Time       `json:"completedAt"`
}

// ForgeCheckEvidence is one check conclusion re-read from forge truth.
type ForgeCheckEvidence struct {
	Name       string          `json:"name"`
	Conclusion CheckConclusion `json:"conclusion"`
}

// ForgeEvidence binds a pull request and its checks to the exact candidate head.
type ForgeEvidence struct {
	Repository       string               `json:"repository"`
	PullRequestID    string               `json:"pullRequestId"`
	HeadRevision     string               `json:"headRevision"`
	CheckConclusions []ForgeCheckEvidence `json:"checkConclusions"`
}

// ReportArtifactEvidence identifies one bounded report body without path authority.
type ReportArtifactEvidence struct {
	ContentHash string `json:"contentHash"`
	Size        int64  `json:"size"`
	MediaType   string `json:"mediaType"`
}

// DeliveryEvidenceBundle is the strict domain input sealed before candidate judgment.
type DeliveryEvidenceBundle struct {
	SchemaVersion           int                         `json:"schemaVersion"`
	TaskHandle              string                      `json:"taskHandle"`
	RepositoryIdentity      string                      `json:"repositoryIdentity"`
	BaseRevision            string                      `json:"baseRevision"`
	HeadRevision            string                      `json:"headRevision"`
	WorktreeCleanliness     WorktreeCleanliness         `json:"worktreeCleanliness"`
	ValidationReceipts      []ValidationEvidenceReceipt `json:"validationReceipts"`
	ForgeEvidence           *ForgeEvidence              `json:"forgeEvidence,omitempty"`
	ReportArtifact          *ReportArtifactEvidence     `json:"reportArtifact,omitempty"`
	UnresolvedDecisionCount int                         `json:"unresolvedDecisionCount"`
	ProducedAt              time.Time                   `json:"producedAt"`
	ExpiresAt               time.Time                   `json:"expiresAt"`
}

// SealedDeliveryEvidence retains canonical immutable bytes and their digest.
type SealedDeliveryEvidence struct {
	bundle    DeliveryEvidenceBundle
	canonical []byte
	digest    string
}

// SealDeliveryEvidence validates, clones, and canonically encodes one bundle.
func SealDeliveryEvidence(input DeliveryEvidenceBundle) (*SealedDeliveryEvidence, error) {
	bundle := cloneDeliveryEvidence(input)
	if err := bundle.validate(); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(bundle)
	if err != nil {
		return nil, errors.New("seal delivery evidence: canonical encoding failed")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(canonical))
	return &SealedDeliveryEvidence{bundle: bundle, canonical: canonical, digest: digest}, nil
}

// ParseDeliveryEvidence validates exact canonical bytes and their expected digest.
func ParseDeliveryEvidence(canonical []byte, expectedDigest string) (*SealedDeliveryEvidence, error) {
	if err := validateSHA256("evidenceDigest", expectedDigest); err != nil || len(canonical) == 0 || len(canonical) > 1<<20 {
		return nil, errors.New("parse delivery evidence: bytes or digest are invalid")
	}
	if fmt.Sprintf("%x", sha256.Sum256(canonical)) != expectedDigest {
		return nil, errors.New("parse delivery evidence: content digest differs")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var bundle DeliveryEvidenceBundle
	if err := decoder.Decode(&bundle); err != nil {
		return nil, errors.New("parse delivery evidence: content is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("parse delivery evidence: trailing content is invalid")
	}
	sealed, err := SealDeliveryEvidence(bundle)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(sealed.canonical, canonical) || sealed.digest != expectedDigest {
		return nil, errors.New("parse delivery evidence: content is not canonical")
	}
	return sealed, nil
}

// Digest returns the immutable evidence identity.
func (sealed *SealedDeliveryEvidence) Digest() string {
	if sealed == nil {
		return ""
	}
	return sealed.digest
}

// Canonical returns an isolated copy of the exact durable bytes.
func (sealed *SealedDeliveryEvidence) Canonical() []byte {
	if sealed == nil {
		return nil
	}
	return append([]byte(nil), sealed.canonical...)
}

// Bundle returns an isolated value for pure domain judgment.
func (sealed *SealedDeliveryEvidence) Bundle() DeliveryEvidenceBundle {
	if sealed == nil {
		return DeliveryEvidenceBundle{}
	}
	return cloneDeliveryEvidence(sealed.bundle)
}

func (bundle DeliveryEvidenceBundle) validate() error {
	validHead := revisionPattern.MatchString(bundle.HeadRevision) ||
		(bundle.WorktreeCleanliness == WorktreeUnknown && bundle.HeadRevision == "")
	if bundle.SchemaVersion != 1 || validateOpaqueID("taskHandle", bundle.TaskHandle) != nil ||
		validateOpaqueID("repositoryIdentity", bundle.RepositoryIdentity) != nil ||
		!revisionPattern.MatchString(bundle.BaseRevision) || !validHead {
		return errors.New("seal delivery evidence: bundle identity is invalid")
	}
	if bundle.WorktreeCleanliness != WorktreeClean && bundle.WorktreeCleanliness != WorktreeDirty && bundle.WorktreeCleanliness != WorktreeUnknown {
		return errors.New("seal delivery evidence: worktree posture is invalid")
	}
	if len(bundle.ValidationReceipts) > 64 ||
		(len(bundle.ValidationReceipts) == 0 && bundle.WorktreeCleanliness == WorktreeClean && bundle.HeadRevision != bundle.BaseRevision) {
		return errors.New("seal delivery evidence: validation receipts are invalid")
	}
	receipts := make(map[string]struct{}, len(bundle.ValidationReceipts))
	for _, receipt := range bundle.ValidationReceipts {
		if err := validateValidationReceipt(receipt, bundle.ProducedAt); err != nil {
			return err
		}
		if _, exists := receipts[receipt.CheckID]; exists {
			return errors.New("seal delivery evidence: validation receipt is duplicated")
		}
		receipts[receipt.CheckID] = struct{}{}
	}
	if bundle.ForgeEvidence != nil && bundle.ReportArtifact != nil {
		return errors.New("seal delivery evidence: delivery artifact is ambiguous")
	}
	if bundle.ForgeEvidence != nil {
		if err := validateForgeEvidence(*bundle.ForgeEvidence); err != nil {
			return err
		}
	}
	if bundle.ReportArtifact != nil {
		if validateSHA256("reportContentHash", bundle.ReportArtifact.ContentHash) != nil ||
			bundle.ReportArtifact.Size < 1 || bundle.ReportArtifact.Size > 1<<30 ||
			!mediaTypePattern.MatchString(bundle.ReportArtifact.MediaType) {
			return errors.New("seal delivery evidence: report artifact is invalid")
		}
	}
	if bundle.UnresolvedDecisionCount < 0 || bundle.UnresolvedDecisionCount > 1024 ||
		bundle.ProducedAt.IsZero() || bundle.ExpiresAt.IsZero() ||
		bundle.ProducedAt.Location() != time.UTC || bundle.ExpiresAt.Location() != time.UTC ||
		!bundle.ExpiresAt.After(bundle.ProducedAt) {
		return errors.New("seal delivery evidence: lifetime or decision count is invalid")
	}
	return nil
}

func validateValidationReceipt(receipt ValidationEvidenceReceipt, producedAt time.Time) error {
	if validateOpaqueID("checkId", receipt.CheckID) != nil || validateOpaqueID("programId", receipt.ProgramID) != nil ||
		!revisionPattern.MatchString(receipt.HeadRevision) || !receipt.Conclusion.valid() ||
		validateSHA256("outputHash", receipt.OutputHash) != nil || receipt.StartedAt.IsZero() || receipt.CompletedAt.IsZero() ||
		receipt.StartedAt.Location() != time.UTC || receipt.CompletedAt.Location() != time.UTC ||
		receipt.CompletedAt.Before(receipt.StartedAt) || receipt.CompletedAt.After(producedAt) {
		return errors.New("seal delivery evidence: validation receipt is invalid")
	}
	return nil
}

func validateForgeEvidence(evidence ForgeEvidence) error {
	if validateOpaqueID("forgeRepository", evidence.Repository) != nil ||
		validateAuthorityReference("pullRequestId", evidence.PullRequestID) != nil ||
		!revisionPattern.MatchString(evidence.HeadRevision) || len(evidence.CheckConclusions) == 0 || len(evidence.CheckConclusions) > 64 {
		return errors.New("seal delivery evidence: forge evidence is invalid")
	}
	names := make(map[string]struct{}, len(evidence.CheckConclusions))
	for _, check := range evidence.CheckConclusions {
		if check.Name == "" || len(check.Name) > 128 || strings.TrimSpace(check.Name) != check.Name ||
			strings.ContainsAny(check.Name, "\x00\r\n") || !check.Conclusion.valid() {
			return errors.New("seal delivery evidence: forge check is invalid")
		}
		if _, exists := names[check.Name]; exists {
			return errors.New("seal delivery evidence: forge check is duplicated")
		}
		names[check.Name] = struct{}{}
	}
	return nil
}

func (conclusion CheckConclusion) valid() bool {
	return conclusion == CheckPassed || conclusion == CheckFailed || conclusion == CheckPending || conclusion == CheckUnknown
}

func cloneDeliveryEvidence(input DeliveryEvidenceBundle) DeliveryEvidenceBundle {
	cloned := input
	cloned.ValidationReceipts = append([]ValidationEvidenceReceipt(nil), input.ValidationReceipts...)
	if input.ForgeEvidence != nil {
		forge := *input.ForgeEvidence
		forge.CheckConclusions = append([]ForgeCheckEvidence(nil), input.ForgeEvidence.CheckConclusions...)
		cloned.ForgeEvidence = &forge
	}
	if input.ReportArtifact != nil {
		artifact := *input.ReportArtifact
		cloned.ReportArtifact = &artifact
	}
	return cloned
}

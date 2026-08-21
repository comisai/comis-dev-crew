package domain

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maximumContractEntries = 32
const maximumWorkerBriefBytes = 16 * 1024

// WorkerBrief is the canonical private launch contract. Content is the exact
// byte sequence pinned by RevisionHash.
type WorkerBrief struct {
	Revision     int64
	RevisionHash string
	Content      string
}

// Validate proves that Content is bounded UTF-8 and exactly matches its stored
// revision hash. Newlines are allowed because the canonical brief is line based;
// all other control characters are rejected.
func (brief WorkerBrief) Validate() error {
	if brief.Revision < 1 {
		return &ValidationError{Field: "briefRevision", Reason: "must be positive"}
	}
	if err := validateSHA256("briefRevisionHash", brief.RevisionHash); err != nil {
		return err
	}
	if strings.TrimSpace(brief.Content) == "" || len(brief.Content) > maximumWorkerBriefBytes || !utf8.ValidString(brief.Content) {
		return &ValidationError{Field: "briefContent", Reason: "must be bounded nonempty UTF-8"}
	}
	for _, character := range brief.Content {
		if unicode.IsControl(character) && character != '\n' {
			return &ValidationError{Field: "briefContent", Reason: "must not contain non-line control characters"}
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(brief.Content)))
	if digest != brief.RevisionHash {
		return &ValidationError{Field: "briefRevisionHash", Reason: "does not pin the rendered worker brief"}
	}
	return nil
}

// PinBriefRevision validates the contract fields and binds the current brief
// revision to the deterministic rendered content.
func (task Task) PinBriefRevision() (Task, error) {
	pinned := task
	pinned.AcceptanceCriteria = append([]string(nil), task.AcceptanceCriteria...)
	pinned.Constraints = append([]string(nil), task.Constraints...)
	pinned.ConsumedContracts = append([]PinnedContract(nil), task.ConsumedContracts...)
	pinned.BriefRevisionHash = ""
	if err := pinned.validateBriefInputs(); err != nil {
		return task, err
	}
	digest, err := pinned.briefRevisionDigest()
	if err != nil {
		return task, err
	}
	pinned.BriefRevisionHash = digest
	if err := pinned.Validate(); err != nil {
		return task, err
	}
	return pinned, nil
}

// RenderWorkerBrief returns the exact content only when the stored revision
// hash still pins every immutable task-contract field.
func (task Task) RenderWorkerBrief() (WorkerBrief, error) {
	if err := task.Validate(); err != nil {
		return WorkerBrief{}, fmt.Errorf("render worker brief: %w", err)
	}
	content, err := task.renderBriefContent()
	if err != nil {
		return WorkerBrief{}, fmt.Errorf("render worker brief: %w", err)
	}
	return WorkerBrief{Revision: task.BriefRevision, RevisionHash: task.BriefRevisionHash, Content: content}, nil
}

func (task Task) validateBriefInputs() error {
	if task.BriefRevision < 1 {
		return &ValidationError{Field: "briefRevision", Reason: "must be positive"}
	}
	if err := validateContractTextList("acceptanceCriteria", task.AcceptanceCriteria, true); err != nil {
		return err
	}
	if err := validateContractTextList("constraints", task.Constraints, false); err != nil {
		return err
	}
	return task.validateConsumedContracts()
}

func (task Task) validateConsumedContracts() error {
	if len(task.ConsumedContracts) > maximumContractEntries {
		return &ValidationError{Field: "consumedContracts", Reason: "must hold a bounded number of pins"}
	}
	seen := make(map[string]struct{}, len(task.ConsumedContracts))
	for _, pin := range task.ConsumedContracts {
		if err := pin.Validate(); err != nil {
			return err
		}
		if _, exists := seen[pin.ArtifactHandle]; exists {
			// Two pins of one artifact could name two different digests, and
			// there would be no answer to which one the worker was told.
			return &ValidationError{Field: "consumedContracts", Reason: "an artifact may be pinned once"}
		}
		seen[pin.ArtifactHandle] = struct{}{}
	}
	return nil
}

func (task Task) briefRevisionDigest() (string, error) {
	content, err := task.renderBriefContent()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content))), nil
}

func (task Task) renderBriefContent() (string, error) {
	if err := task.validateBriefInputs(); err != nil {
		return "", err
	}
	var content strings.Builder
	writeBriefField(&content, "taskHandle", task.Handle)
	writeBriefField(&content, "briefRevision", strconv.FormatInt(task.BriefRevision, 10))
	writeBriefField(&content, "shape", string(task.Shape))
	writeBriefField(&content, "deliveryMode", string(task.DeliveryMode))
	writeBriefField(&content, "repositoryId", task.RepositoryID)
	writeBriefField(&content, "baseRevision", task.BaseRevision)
	writeBriefField(&content, "validationProfile", task.ValidationProfile)
	writeBriefField(&content, "workerProfileId", task.WorkerProfileID)
	writeBriefList(&content, "acceptanceCriteria", task.AcceptanceCriteria)
	writeBriefList(&content, "constraints", task.Constraints)
	writeBriefContracts(&content, task.ConsumedContracts)
	writeBriefField(&content, "workspaceSelfCheck", "verify the canonical working directory and task handle before mutation")
	writeBriefField(&content, "reportCommand", "devcrew-report through the protected task reporter")
	writeBriefField(&content, "reportKinds", "progress, attention, blocked, paused, candidate_complete, failed, resolution")
	writeBriefField(&content, "decisionProtocol", "request one keyed decision and wait for acknowledged delivery")
	writeBriefField(&content, "completionMeaning", "candidate_complete requires service validation and evidence")
	writeBriefField(&content, "prohibitedActions", "merge, push, change Git remotes, mutate the primary checkout, change task shape, or bypass the reporter")
	return content.String(), nil
}

// writeBriefContracts renders the pins in sorted order, so the same set listed
// differently yields the same brief. Ordering is not part of the contract, and a
// revision hash that churned on it would signal a change that never happened.
func writeBriefContracts(destination *strings.Builder, pins []PinnedContract) {
	if len(pins) == 0 {
		return
	}
	sorted := append([]PinnedContract(nil), pins...)
	sort.Slice(sorted, func(left, right int) bool {
		return sorted[left].ArtifactHandle < sorted[right].ArtifactHandle
	})
	destination.WriteString("consumedContracts:\n")
	for _, pin := range sorted {
		destination.WriteString("  - ")
		destination.WriteString(pin.ArtifactHandle)
		destination.WriteString(" ")
		destination.WriteString(string(pin.Kind))
		destination.WriteString(" ")
		destination.WriteString(pin.ContentHash)
		destination.WriteByte('\n')
	}
}

func writeBriefField(destination *strings.Builder, name, value string) {
	destination.WriteString(name)
	destination.WriteString(": ")
	destination.WriteString(value)
	destination.WriteByte('\n')
}

func writeBriefList(destination *strings.Builder, name string, values []string) {
	destination.WriteString(name)
	destination.WriteString(":\n")
	for _, value := range values {
		destination.WriteString("- ")
		destination.WriteString(strconv.Quote(value))
		destination.WriteByte('\n')
	}
}

func validateContractTextList(field string, values []string, required bool) error {
	if required && len(values) == 0 {
		return &ValidationError{Field: field, Reason: "must contain at least one entry"}
	}
	if len(values) > maximumContractEntries {
		return &ValidationError{Field: field, Reason: "must contain at most 32 entries"}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateSafeText(field, value); err != nil {
			return err
		}
		if _, exists := seen[value]; exists {
			return &ValidationError{Field: field, Reason: "must not contain duplicate entries"}
		}
		seen[value] = struct{}{}
	}
	return nil
}

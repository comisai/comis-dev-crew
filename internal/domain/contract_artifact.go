package domain

import (
	"sort"
	"time"
)

// maxContractArtifactBytes bounds one contract artifact. A contract is an
// interface two components agree on, not a payload; anything larger is a
// delivery, and deliveries have their own path.
const maxContractArtifactBytes = 1 << 20

// ComponentContractArtifact is how independently running components agree on an
// interface without sharing a worktree.
//
// Artifacts are immutable and always digested. That is the whole mechanism: a
// consumer pins the exact handle and digest in its brief, so a change to the
// contract is a NEW artifact that supersedes the old one, never an edit that
// silently rewrites what a running worker was told.
type ComponentContractArtifact struct {
	ArtifactHandle           string
	InitiativeHandle         string
	ProducerTaskHandle       string
	Kind                     ContractArtifactKind
	ContentHash              string
	SourceRevision           string
	MediaType                string
	Size                     int64
	ProducedAt               time.Time
	SupersedesArtifactHandle string
}

// Validate enforces the immutable, bounded, digested contract record.
func (artifact ComponentContractArtifact) Validate() error {
	if err := validateOpaqueID("artifactHandle", artifact.ArtifactHandle); err != nil {
		return err
	}
	if err := validateOpaqueID("initiativeHandle", artifact.InitiativeHandle); err != nil {
		return err
	}
	if err := ValidateTaskHandle(artifact.ProducerTaskHandle); err != nil {
		return err
	}
	if !artifact.Kind.valid() {
		return &ValidationError{Field: "kind", Reason: "must be a closed contract artifact kind"}
	}
	// The digest is what makes the artifact pinnable. Without it a downstream
	// brief could name a contract whose content had already moved underneath it.
	if err := validateSHA256("contentHash", artifact.ContentHash); err != nil {
		return err
	}
	if err := validateRevision(artifact.SourceRevision); err != nil {
		return err
	}
	if !mediaTypePattern.MatchString(artifact.MediaType) {
		return &ValidationError{Field: "mediaType", Reason: "must be a bounded media type"}
	}
	if artifact.Size <= 0 || artifact.Size > maxContractArtifactBytes {
		return &ValidationError{Field: "size", Reason: "must be a positive bounded artifact size"}
	}
	if artifact.SupersedesArtifactHandle != "" {
		if err := validateOpaqueID("supersedesArtifactHandle", artifact.SupersedesArtifactHandle); err != nil {
			return err
		}
		if artifact.SupersedesArtifactHandle == artifact.ArtifactHandle {
			return &ValidationError{
				Field:  "supersedesArtifactHandle",
				Reason: "an artifact cannot supersede itself",
			}
		}
	}
	return nil
}

// TasksStaleAfterSupersession names the members whose pinned contract just
// stopped being current, and only those.
//
// The precision is the point. A task that merely depends on the producer — an
// integration lane waiting on it, say — consumes no artifact from it, so its
// evidence is unaffected and staling it would throw away valid work. Only a
// consumer that pinned this artifact kind from this producer goes stale.
func (initiative DevelopmentInitiative) TasksStaleAfterSupersession(
	kind ContractArtifactKind,
	producerTaskHandle string,
) []string {
	stale := make([]string, 0, len(initiative.Edges))
	seen := make(map[string]struct{}, len(initiative.Edges))
	for _, edge := range initiative.Edges {
		if edge.Kind != EdgeConsumesArtifact ||
			edge.FromTaskHandle != producerTaskHandle ||
			edge.RequiredArtifactKind != kind {
			continue
		}
		if _, exists := seen[edge.ToTaskHandle]; exists {
			continue
		}
		seen[edge.ToTaskHandle] = struct{}{}
		stale = append(stale, edge.ToTaskHandle)
	}
	sort.Strings(stale)
	return stale
}

// TasksStaleAfterHeadChange names the members whose evidence stopped meaning
// anything when one task's head moved.
//
// Two groups, and only two. The producer itself, because its validation
// evidence was gathered against content that no longer exists; and whoever
// pinned an artifact it produced, because that artifact was built from the old
// head. A lane that merely waits on the producer — an integration step, say —
// consumed nothing from it, so its evidence is about a different question and
// survives.
//
// A task outside the initiative stales nothing: a head move in another
// initiative is not this initiative's business.
func (initiative DevelopmentInitiative) TasksStaleAfterHeadChange(taskHandle string) []string {
	if !initiative.contains(taskHandle) {
		return nil
	}
	stale := []string{taskHandle}
	seen := map[string]struct{}{taskHandle: {}}
	for _, edge := range initiative.Edges {
		if edge.Kind != EdgeConsumesArtifact || edge.FromTaskHandle != taskHandle {
			continue
		}
		if _, exists := seen[edge.ToTaskHandle]; exists {
			continue
		}
		seen[edge.ToTaskHandle] = struct{}{}
		stale = append(stale, edge.ToTaskHandle)
	}
	sort.Strings(stale[1:])
	return stale
}

func (initiative DevelopmentInitiative) contains(taskHandle string) bool {
	for _, component := range initiative.Components {
		for _, handle := range component.TaskHandles {
			if handle == taskHandle {
				return true
			}
		}
	}
	return false
}

// PinnedContract is one contract a task consumes, named by handle AND digest.
//
// The digest is what makes the handoff immutable in practice. A brief carrying
// only a handle could be answered with different bytes under the same name, and
// the consumer would have no way to notice; carrying the digest means a
// superseded contract cannot ride into a running worker unannounced.
type PinnedContract struct {
	ArtifactHandle string
	Kind           ContractArtifactKind
	ContentHash    string
}

// Validate enforces one pinned contract reference.
func (pin PinnedContract) Validate() error {
	if err := validateOpaqueID("consumedContracts.artifactHandle", pin.ArtifactHandle); err != nil {
		return err
	}
	if !pin.Kind.valid() {
		return &ValidationError{
			Field:  "consumedContracts.kind",
			Reason: "must be a closed contract artifact kind",
		}
	}
	return validateSHA256("consumedContracts.contentHash", pin.ContentHash)
}

package domain

import (
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"
)

// maxRequestedOutcomeBytes bounds the request text. A backlog item states what
// is wanted; it is not the brief, and it is not a place to smuggle instructions
// a worker would later execute.
const maxRequestedOutcomeBytes = 8192

// BacklogPriority is the closed ordering vocabulary.
type BacklogPriority string

const (
	BacklogPriorityLow    BacklogPriority = "low"
	BacklogPriorityNormal BacklogPriority = "normal"
	BacklogPriorityHigh   BacklogPriority = "high"
)

func (priority BacklogPriority) valid() bool {
	switch priority {
	case BacklogPriorityLow, BacklogPriorityNormal, BacklogPriorityHigh:
		return true
	}
	return false
}

// BacklogReadiness is the closed readiness vocabulary. Promoted is terminal for
// the record: a request becomes work exactly once.
type BacklogReadiness string

const (
	BacklogNeedsRefinement BacklogReadiness = "needs_refinement"
	BacklogReady           BacklogReadiness = "ready"
	BacklogPromoted        BacklogReadiness = "promoted"
	BacklogDropped         BacklogReadiness = "dropped"
)

func (readiness BacklogReadiness) valid() bool {
	switch readiness {
	case BacklogNeedsRefinement, BacklogReady, BacklogPromoted, BacklogDropped:
		return true
	}
	return false
}

// BacklogItem is one durable request that has not become work yet.
//
// The record deliberately has no field for a managed run, workspace lease,
// attachment, credential or delivery mode. That absence IS the authority
// boundary: a backlog item cannot carry run authority because there is nowhere
// to put it, and promotion has to go through the normal two-phase flow to
// obtain any.
type BacklogItem struct {
	SchemaVersion         int
	Handle                string
	RepositoryID          string
	Shape                 TaskShape
	RequestedOutcome      string
	DependsOn             []string
	Priority              BacklogPriority
	Readiness             BacklogReadiness
	SourceConversationRef string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Validate enforces the strict backlog record.
func (item BacklogItem) Validate() error {
	if item.SchemaVersion != 1 {
		return &ValidationError{Field: "schemaVersion", Reason: "must equal 1"}
	}
	if err := validateOpaqueID("backlogHandle", item.Handle); err != nil {
		return err
	}
	if err := ValidateRepositoryID(item.RepositoryID); err != nil {
		return err
	}
	if !item.Shape.valid() {
		return &ValidationError{Field: "shape", Reason: "must be a closed task shape"}
	}
	if !item.Priority.valid() {
		return &ValidationError{Field: "priority", Reason: "must be a closed priority"}
	}
	if !item.Readiness.valid() {
		return &ValidationError{Field: "readiness", Reason: "must be a closed readiness"}
	}
	if item.RequestedOutcome == "" || len(item.RequestedOutcome) > maxRequestedOutcomeBytes ||
		!utf8.ValidString(item.RequestedOutcome) {
		return &ValidationError{Field: "requestedOutcome", Reason: "must be bounded valid text"}
	}
	if err := validateAuthorityReference("sourceConversationRef", item.SourceConversationRef); err != nil {
		return err
	}
	if len(item.DependsOn) > 64 {
		return &ValidationError{Field: "dependsOn", Reason: "must hold at most 64 dependencies"}
	}
	seen := make(map[string]struct{}, len(item.DependsOn))
	for _, dependency := range item.DependsOn {
		if err := validateOpaqueID("dependsOn", dependency); err != nil {
			return err
		}
		if dependency == item.Handle {
			return &ValidationError{Field: "dependsOn", Reason: "an item cannot depend on itself"}
		}
		if _, exists := seen[dependency]; exists {
			return &ValidationError{Field: "dependsOn", Reason: "dependencies must be unique"}
		}
		seen[dependency] = struct{}{}
	}
	if item.UpdatedAt.Before(item.CreatedAt) {
		return &ValidationError{Field: "updatedAt", Reason: "cannot precede creation"}
	}
	return nil
}

// CheckPromotable reports whether one item may become a prepared task now.
//
// Promotion prepares a real managed run, so it is refused for anything that is
// not currently ready with every dependency satisfied — and refused outright for
// an item already promoted, because binding a second run to one request would
// leave the first orphaned.
func (item BacklogItem) CheckPromotable(satisfied map[string]bool) error {
	if err := item.Validate(); err != nil {
		return err
	}
	if item.Readiness != BacklogReady {
		return &ValidationError{
			Field:  "readiness",
			Reason: fmt.Sprintf("only a ready item may be promoted; this one is %s", item.Readiness),
		}
	}
	for _, dependency := range item.DependsOn {
		if !satisfied[dependency] {
			return &ValidationError{
				Field:  "dependsOn",
				Reason: fmt.Sprintf("dependency %s is not satisfied", dependency),
			}
		}
	}
	return nil
}

// BacklogItemFieldNames lists the record's own field names. It exists so the
// authority boundary can be asserted against the SHAPE of the record rather
// than against a validator that could later be relaxed.
func BacklogItemFieldNames(item BacklogItem) []string {
	value := reflect.TypeOf(item)
	names := make([]string, 0, value.NumField())
	for index := 0; index < value.NumField(); index++ {
		names = append(names, value.Field(index).Name)
	}
	return names
}

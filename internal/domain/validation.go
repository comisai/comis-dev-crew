package domain

import (
	"regexp"
	"time"
	"unicode/utf8"
)

var (
	opaqueIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	commandPattern   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{2,63}$`)
	revisionPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	authorityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{2,255}$`)
)

func validateOpaqueID(field, value string) error {
	if !opaqueIDPattern.MatchString(value) {
		return &ValidationError{Field: field, Reason: "must be a bounded lowercase opaque identifier"}
	}
	return nil
}

// ValidateTaskHandle rejects task references that are not bounded opaque IDs.
func ValidateTaskHandle(value string) error {
	return validateOpaqueID("taskHandle", value)
}

// ValidateRepositoryID rejects repository references that are not bounded
// opaque IDs. It delegates to the same rule task records are validated with, so
// a caller-supplied repository cannot be accepted by one surface and refused by
// another.
func ValidateRepositoryID(value string) error {
	return validateOpaqueID("repositoryId", value)
}

// ValidateOperationID rejects operation references that are not bounded opaque IDs.
func ValidateOperationID(value string) error {
	return validateOpaqueID("operationId", value)
}

// ValidateBriefRevisionHash rejects values that are not lowercase SHA-256 digests.
func ValidateBriefRevisionHash(value string) error {
	return validateSHA256("briefRevisionHash", value)
}

// ValidateGitRevision rejects values that are not full lowercase Git object identities.
func ValidateGitRevision(value string) error {
	return validateRevision(value)
}

// ValidateLocalReportID rejects report identities outside the bounded opaque form.
func ValidateLocalReportID(value string) error {
	return validateOpaqueID("localReportId", value)
}

// ValidateDecisionKey rejects decision identities outside the bounded opaque form.
func ValidateDecisionKey(value string) error {
	return validateOpaqueID("externalKey", value)
}

func validateCommand(value string) error {
	if !commandPattern.MatchString(value) {
		return &ValidationError{Field: "command", Reason: "must be a known command-style identifier"}
	}
	return nil
}

func validateAuthorityReference(field, value string) error {
	if !authorityPattern.MatchString(value) {
		return &ValidationError{Field: field, Reason: "must be a bounded opaque authority reference"}
	}
	return nil
}

// ValidateAuthorityReference rejects malformed host-owned opaque identities.
func ValidateAuthorityReference(field, value string) error {
	return validateAuthorityReference(field, value)
}

func validateRevision(value string) error {
	if !revisionPattern.MatchString(value) {
		return &ValidationError{Field: "baseRevision", Reason: "must be a lowercase Git object identity"}
	}
	return nil
}

// ValidateTaskState rejects a task state outside the closed set.
//
// Scoping a read by state must refuse an unknown one rather than quietly
// matching nothing, which would read as "no such work" instead of "no such
// state".
func ValidateTaskState(value TaskState) error {
	if !value.valid() {
		return &ValidationError{Field: "state", Reason: "must be a known task state"}
	}
	return nil
}

// MaximumDecisionResponseBytes bounds one answer before it is stored, so an
// oversized reply is refused rather than truncated into a different answer.
const MaximumDecisionResponseBytes = 8192

// ValidateDecisionResponse rejects an answer that is empty, oversized, not valid
// text, or carries control characters.
//
// The answer is human input that a worker will read, so a control sequence in it
// would reach a terminal as a command rather than as text. Refusing is right
// rather than escaping: an answer nobody can read as written is worth stopping
// on, not silently rewriting into a different one.
func ValidateDecisionResponse(value string) error {
	if value == "" {
		return &ValidationError{Field: "response", Reason: "must not be empty"}
	}
	if len(value) > MaximumDecisionResponseBytes {
		return &ValidationError{Field: "response", Reason: "must stay within its byte bound"}
	}
	if !utf8.ValidString(value) {
		return &ValidationError{Field: "response", Reason: "must be valid text"}
	}
	for _, character := range value {
		if character == '\n' || character == '\t' {
			continue
		}
		if character < 0x20 || character == 0x7f {
			return &ValidationError{Field: "response", Reason: "must not carry control characters"}
		}
	}
	return nil
}

func validateSHA256(field, value string) error {
	if !sha256HexPattern.MatchString(value) {
		return &ValidationError{Field: field, Reason: "must be a lowercase SHA-256 digest"}
	}
	return nil
}

func validateTimes(createdAt, updatedAt time.Time) error {
	if createdAt.IsZero() || updatedAt.IsZero() {
		return &ValidationError{Field: "timestamps", Reason: "must be present"}
	}
	if createdAt.Location() != time.UTC || updatedAt.Location() != time.UTC {
		return &ValidationError{Field: "timestamps", Reason: "must use UTC"}
	}
	if updatedAt.Before(createdAt) {
		return &ValidationError{Field: "updatedAt", Reason: "must not precede createdAt"}
	}
	return nil
}

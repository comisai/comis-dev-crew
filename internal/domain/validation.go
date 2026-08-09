package domain

import (
	"regexp"
	"time"
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

// ValidateOperationID rejects operation references that are not bounded opaque IDs.
func ValidateOperationID(value string) error {
	return validateOpaqueID("operationId", value)
}

// ValidateBriefRevisionHash rejects values that are not lowercase SHA-256 digests.
func ValidateBriefRevisionHash(value string) error {
	return validateSHA256("briefRevisionHash", value)
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

package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrorCode is a closed safe failure classification shared by application
// handlers and transport adapters.
type ErrorCode string

const (
	ErrorInvalidArgument  ErrorCode = "invalid_argument"
	ErrorNotFound         ErrorCode = "not_found"
	ErrorConflict         ErrorCode = "conflict"
	ErrorUnauthorized     ErrorCode = "unauthorized"
	ErrorUnavailable      ErrorCode = "unavailable"
	ErrorDeadlineExceeded ErrorCode = "deadline_exceeded"
	ErrorPrecondition     ErrorCode = "precondition"
	ErrorInternal         ErrorCode = "internal"
	ErrorUnknown          ErrorCode = "unknown"
)

// Valid reports whether the code is part of the current closed contract.
func (code ErrorCode) Valid() bool {
	switch code {
	case ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorUnauthorized,
		ErrorUnavailable, ErrorDeadlineExceeded, ErrorPrecondition, ErrorInternal,
		ErrorUnknown:
		return true
	default:
		return false
	}
}

// Failure is safe to translate at a transport boundary. Error deliberately
// excludes the wrapped adapter cause so credentials and private content cannot
// become protocol or log text by accident.
type Failure struct {
	Code       ErrorCode
	Retryable  bool
	Message    string
	Hint       string
	underlying error
}

// NewFailure validates the closed code and bounded operator-facing text.
func NewFailure(code ErrorCode, retryable bool, message, hint string, cause error) (*Failure, error) {
	if !code.Valid() {
		return nil, &ValidationError{Field: "code", Reason: "must be a known error code"}
	}
	if err := validateSafeText("message", message); err != nil {
		return nil, err
	}
	if err := validateSafeText("hint", hint); err != nil {
		return nil, err
	}
	return &Failure{Code: code, Retryable: retryable, Message: message, Hint: hint, underlying: cause}, nil
}

func (failure *Failure) Error() string {
	return fmt.Sprintf("%s: %s", failure.Code, failure.Message)
}

// Unwrap preserves the private cause for internal errors.Is/errors.As checks.
func (failure *Failure) Unwrap() error {
	return failure.underlying
}

// ValidationError identifies one invalid domain field without echoing its
// untrusted value.
type ValidationError struct {
	Field  string
	Reason string
}

func (failure *ValidationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", failure.Field, failure.Reason)
}

func validateSafeText(field, value string) error {
	return validateBoundedSafeText(field, value, 512)
}

func validateBoundedSafeText(field, value string, maximumBytes int) error {
	if strings.TrimSpace(value) == "" {
		return &ValidationError{Field: field, Reason: "must not be empty"}
	}
	if len(value) > maximumBytes {
		return &ValidationError{Field: field, Reason: fmt.Sprintf("must be at most %d bytes", maximumBytes)}
	}
	if !utf8.ValidString(value) {
		return &ValidationError{Field: field, Reason: "must be valid UTF-8"}
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return &ValidationError{Field: field, Reason: "must not contain control characters"}
		}
	}
	return nil
}

package domain

import "time"

// OperationStatus describes the durable outcome of one idempotent request.
type OperationStatus string

const (
	OperationAccepted  OperationStatus = "accepted"
	OperationCompleted OperationStatus = "completed"
	OperationRejected  OperationStatus = "rejected"
	OperationUnknown   OperationStatus = "unknown"
)

func (status OperationStatus) valid() bool {
	switch status {
	case OperationAccepted, OperationCompleted, OperationRejected, OperationUnknown:
		return true
	default:
		return false
	}
}

// OperationRecord is the content-free replay ledger entry for a canonical
// command. SubjectDigest binds an operation ID to the exact normalized input.
type OperationRecord struct {
	SchemaVersion int
	ID            string
	Command       string
	SubjectDigest string
	Status        OperationStatus
	ErrorCode     ErrorCode
	ResultRef     string
	StateVersion  int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Validate enforces identity, replay digest, status, and time invariants.
func (operation OperationRecord) Validate() error {
	if operation.SchemaVersion != 1 {
		return &ValidationError{Field: "schemaVersion", Reason: "must equal 1"}
	}
	if err := validateOpaqueID("operationId", operation.ID); err != nil {
		return err
	}
	if err := validateCommand(operation.Command); err != nil {
		return err
	}
	if err := validateSHA256("subjectDigest", operation.SubjectDigest); err != nil {
		return err
	}
	if !operation.Status.valid() {
		return &ValidationError{Field: "status", Reason: "must be a known operation status"}
	}
	if operation.Status == OperationRejected {
		if !operation.ErrorCode.Valid() {
			return &ValidationError{Field: "errorCode", Reason: "rejected operations require a known code"}
		}
	} else if operation.ErrorCode != "" {
		return &ValidationError{Field: "errorCode", Reason: "only rejected operations carry an error code"}
	}
	if operation.ResultRef != "" {
		if err := validateOpaqueID("resultRef", operation.ResultRef); err != nil {
			return err
		}
	}
	if operation.StateVersion < 1 {
		return &ValidationError{Field: "stateVersion", Reason: "must be positive"}
	}
	return validateTimes(operation.CreatedAt, operation.UpdatedAt)
}

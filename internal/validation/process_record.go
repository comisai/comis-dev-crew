package validation

import (
	"errors"
	"regexp"
	"time"
)

var processIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{0,255}$`)

// Validate rejects contentful, incomplete, or contradictory process evidence.
func (record ProcessRecord) Validate() error {
	if !identifierPattern.MatchString(record.TaskHandle) || !identifierPattern.MatchString(record.OperationID) ||
		!identifierPattern.MatchString(record.ProgramID) || !processIdentityPattern.MatchString(record.ExecutableLabel) {
		return errors.New("validate validation process: record identity is invalid")
	}
	if record.StartedAt.IsZero() || record.ObservedAt.IsZero() ||
		record.StartedAt.Location() != time.UTC || record.ObservedAt.Location() != time.UTC ||
		record.ObservedAt.Before(record.StartedAt) {
		return errors.New("validate validation process: observation time is invalid")
	}
	hasProcessIdentity := record.PID > 0 && processIdentityPattern.MatchString(record.StartIdentity) &&
		processIdentityPattern.MatchString(record.ProcessGroupIdentity)
	switch record.State {
	case ProcessStarting:
		if record.PID != 0 || record.StartIdentity != "" || record.ProcessGroupIdentity != "" || record.ExitCode != nil {
			return errors.New("validate validation process: starting evidence is contradictory")
		}
	case ProcessRunning:
		if !hasProcessIdentity || record.ExitCode != nil {
			return errors.New("validate validation process: running evidence is incomplete")
		}
	case ProcessExited:
		if !hasProcessIdentity {
			return errors.New("validate validation process: exited evidence is incomplete")
		}
	case ProcessUnknown:
		if record.ExitCode != nil || (record.PID != 0 && !hasProcessIdentity) {
			return errors.New("validate validation process: unknown evidence is incomplete")
		}
	default:
		return errors.New("validate validation process: state is unknown")
	}
	return nil
}

// CanFollow proves a monotonic observation for one immutable process identity.
func (record ProcessRecord) CanFollow(previous ProcessRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if err := previous.Validate(); err != nil {
		return errors.New("advance validation process: durable record is invalid")
	}
	if record.TaskHandle != previous.TaskHandle || record.OperationID != previous.OperationID ||
		record.ProgramID != previous.ProgramID || record.ExecutableLabel != previous.ExecutableLabel ||
		!record.StartedAt.Equal(previous.StartedAt) || record.ObservedAt.Before(previous.ObservedAt) {
		return errors.New("advance validation process: immutable identity or time changed")
	}
	if previous.State == record.State {
		if previous.PID != record.PID || previous.StartIdentity != record.StartIdentity ||
			previous.ProcessGroupIdentity != record.ProcessGroupIdentity || !equalExitCode(previous.ExitCode, record.ExitCode) {
			return errors.New("advance validation process: same-state evidence changed")
		}
		return nil
	}
	if previous.State == ProcessStarting && record.State == ProcessRunning {
		return nil
	}
	if previous.State == ProcessStarting && record.State == ProcessUnknown && record.PID == 0 {
		return nil
	}
	if previous.State == ProcessRunning && (record.State == ProcessExited || record.State == ProcessUnknown) &&
		previous.PID == record.PID && previous.StartIdentity == record.StartIdentity &&
		previous.ProcessGroupIdentity == record.ProcessGroupIdentity {
		return nil
	}
	return errors.New("advance validation process: lifecycle is regressive or ambiguous")
}

func equalExitCode(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

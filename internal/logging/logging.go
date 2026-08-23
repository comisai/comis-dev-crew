// Package logging writes boundary records as structured lines.
//
// Destination is the process's standard error. The service runs as a supervised
// unit, so its stderr is already collected, rotated and retained by the
// supervisor; opening a file here would add a second retention surface with its
// own growth and permissions to get wrong, for a stream the host already keeps.
package logging

import (
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// Level is the closed operator-selectable verbosity.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// ParseLevel resolves one operator-supplied level, refusing anything else.
// An unknown level is refused rather than defaulted, because a service that
// silently ran at a different verbosity than the operator asked for is exactly
// the surprise this configuration exists to avoid.
func ParseLevel(value string) (Level, error) {
	switch Level(strings.ToLower(strings.TrimSpace(value))) {
	case LevelDebug:
		return LevelDebug, nil
	case LevelInfo:
		return LevelInfo, nil
	case LevelWarn:
		return LevelWarn, nil
	case LevelError:
		return LevelError, nil
	default:
		return "", errors.New("parse log level: level must be debug, info, warn, or error")
	}
}

func (level Level) slogLevel() slog.Level {
	switch level {
	case LevelDebug:
		return slog.LevelDebug
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Logger writes each boundary record as one structured line.
type Logger struct {
	log *slog.Logger
}

// New builds a logger over the given destination at one fixed level.
func New(destination io.Writer, level Level) (*Logger, error) {
	if destination == nil {
		return nil, errors.New("create logger: destination is required")
	}
	if _, err := ParseLevel(string(level)); err != nil {
		return nil, err
	}
	handler := slog.NewJSONHandler(destination, &slog.HandlerOptions{Level: level.slogLevel()})
	return &Logger{log: slog.New(handler)}, nil
}

// Record writes one crossing.
//
// A step is DEBUG because it exists to locate a call that never finished, and a
// failure separates retryable from terminal: an operator paging on ERROR should
// not be woken by a forge that will succeed on the next poll.
func (logger *Logger) Record(record application.BoundaryRecord) {
	if logger == nil || logger.log == nil {
		return
	}
	attributes := []any{
		slog.String("boundary", string(record.Boundary)),
		slog.String("operation", record.Operation),
		slog.Int64("durationMs", record.DurationMs),
		slog.String("outcome", string(record.Outcome)),
	}
	if record.OperationID != "" {
		attributes = append(attributes, slog.String("operationId", record.OperationID))
	}
	if record.TaskHandle != "" {
		attributes = append(attributes, slog.String("taskHandle", record.TaskHandle))
	}
	if record.InitiativeHandle != "" {
		attributes = append(attributes, slog.String("initiativeHandle", record.InitiativeHandle))
	}
	if record.ManagedRunGroupID != "" {
		attributes = append(attributes, slog.String("managedRunGroupId", record.ManagedRunGroupID))
	}
	if record.AttemptCount > 0 {
		attributes = append(attributes, slog.Int("attemptCount", record.AttemptCount))
	}
	switch record.Outcome {
	case application.BoundaryStep:
		logger.log.Debug("boundary step", attributes...)
	case application.BoundaryFailed:
		attributes = append(attributes,
			slog.String("errorKind", string(record.ErrorKind)),
			slog.String("hint", record.Hint),
		)
		if record.FailureCause != "" {
			attributes = append(attributes, slog.String("failureCause", string(record.FailureCause)))
		}
		if record.HostProjectionMismatch != "" {
			attributes = append(attributes,
				slog.String("hostProjectionMismatch", string(record.HostProjectionMismatch)),
				slog.Any("expectedHostStateCounts", record.ExpectedHostStateCounts),
				slog.Any("observedHostStateCounts", record.ObservedHostStateCounts),
			)
		}
		logger.log.Error("boundary failed", attributes...)
	default:
		logger.log.Info("boundary completed", attributes...)
	}
}

var _ application.BoundaryLogger = (*Logger)(nil)

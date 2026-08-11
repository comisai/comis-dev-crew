package validation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// ProcessState is the closed service-owned validation-process lifecycle.
type ProcessState string

const (
	ProcessStarting ProcessState = "starting"
	ProcessRunning  ProcessState = "running"
	ProcessExited   ProcessState = "exited"
	ProcessUnknown  ProcessState = "unknown"
)

// ProcessRecord is content-free durable validation-process evidence.
type ProcessRecord struct {
	TaskHandle           string
	OperationID          string
	ProgramID            string
	ExecutableLabel      string
	PID                  int
	StartIdentity        string
	ProcessGroupIdentity string
	State                ProcessState
	StartedAt            time.Time
	ObservedAt           time.Time
	ExitCode             *int
}

// ProcessStore persists each validation-process observation before it is used.
type ProcessStore interface {
	Record(context.Context, ProcessRecord) error
}

// RunRequest binds one validation operation to exact task-owned fields.
type RunRequest struct {
	OperationID string
	TaskHandle  string
	ProfileID   string
	CheckID     string
	Fields      TaskFields
}

// Receipt is immutable evidence for one completed local validation check.
type Receipt struct {
	OperationID  string
	TaskHandle   string
	ProfileID    string
	CheckID      string
	ProgramID    string
	HeadRevision string
	StartedAt    time.Time
	CompletedAt  time.Time
	ExitCode     int
	Passed       bool
	OutputHash   string
	OutputBytes  int64
}

// RunnerConfig supplies the reviewed catalog and durable process registry.
type RunnerConfig struct {
	Catalog        *Catalog
	Processes      ProcessStore
	MaxOutputBytes int64
	Clock          func() time.Time
}

type activeProcess struct {
	taskHandle string
	command    *exec.Cmd
}

// Runner owns all E0 validation subprocesses and their process groups.
type Runner struct {
	catalog        *Catalog
	processes      ProcessStore
	maxOutputBytes int64
	clock          func() time.Time
	mu             sync.Mutex
	active         map[string]activeProcess
}

// NewRunner constructs the sole service-owned validation process registry.
func NewRunner(config RunnerConfig) (*Runner, error) {
	if config.Catalog == nil || config.Processes == nil || config.MaxOutputBytes < 1 || config.MaxOutputBytes > 16<<20 {
		return nil, errors.New("create validation runner: catalog, process store, and bounded output are required")
	}
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{
		catalog: config.Catalog, processes: config.Processes, maxOutputBytes: config.MaxOutputBytes,
		clock: clock, active: make(map[string]activeProcess),
	}, nil
}

// Run starts one fixed program in its own process group and records every state.
func (runner *Runner) Run(ctx context.Context, request RunRequest) (Receipt, error) {
	if runner == nil || ctx == nil {
		return Receipt{}, errors.New("run validation: runner and context are required")
	}
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if !identifierPattern.MatchString(request.OperationID) || request.TaskHandle != request.Fields.TaskHandle {
		return Receipt{}, errors.New("run validation: request identity is invalid")
	}
	resolved, err := runner.catalog.ResolveLocalCheck(request.ProfileID, request.CheckID, request.Fields)
	if err != nil {
		return Receipt{}, err
	}
	runContext, cancel := context.WithTimeout(ctx, resolved.Timeout)
	defer cancel()
	command := exec.CommandContext(runContext, resolved.Executable, resolved.Arguments...)
	command.Dir = resolved.WorkingDirectory
	command.Env = []string{}
	command.SysProcAttr = processGroupAttributes()
	output := newBoundedHashWriter(runner.maxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	startedAt := runner.clock()
	starting := ProcessRecord{
		TaskHandle: request.TaskHandle, OperationID: request.OperationID,
		ProgramID: resolved.ProgramID, ExecutableLabel: resolved.ProgramID,
		State: ProcessStarting, StartedAt: startedAt, ObservedAt: startedAt,
	}
	if err := runner.processes.Record(ctx, starting); err != nil {
		return Receipt{}, fmt.Errorf("run validation: record starting process: %w", err)
	}
	if err := runner.register(request.OperationID, request.TaskHandle, command); err != nil {
		return Receipt{}, err
	}
	defer runner.unregister(request.OperationID)
	if err := command.Start(); err != nil {
		return Receipt{}, errors.New("run validation: fixed program did not start")
	}
	startIdentity := processStartIdentity(request.OperationID, command.Process.Pid, startedAt)
	running := starting
	running.PID = command.Process.Pid
	running.StartIdentity = startIdentity
	running.ProcessGroupIdentity = strconv.Itoa(command.Process.Pid)
	running.State = ProcessRunning
	running.ObservedAt = runner.clock()
	if err := runner.processes.Record(context.WithoutCancel(ctx), running); err != nil {
		_ = terminateProcessGroup(command.Process.Pid)
		_ = command.Wait()
		return Receipt{}, fmt.Errorf("run validation: record running process: %w", err)
	}
	waitErr := command.Wait()
	completedAt := runner.clock()
	exitCode := command.ProcessState.ExitCode()
	exited := running
	exited.State = ProcessExited
	exited.ObservedAt = completedAt
	exited.ExitCode = &exitCode
	if err := runner.processes.Record(context.WithoutCancel(ctx), exited); err != nil {
		return Receipt{}, fmt.Errorf("run validation: record exited process: %w", err)
	}
	receipt := Receipt{
		OperationID: request.OperationID, TaskHandle: request.TaskHandle,
		ProfileID: request.ProfileID, CheckID: request.CheckID, ProgramID: resolved.ProgramID,
		HeadRevision: request.Fields.HeadRevision, StartedAt: startedAt, CompletedAt: completedAt,
		ExitCode: exitCode, Passed: waitErr == nil && !output.exceeded,
		OutputHash: output.sum(), OutputBytes: output.bytes,
	}
	if output.exceeded {
		return receipt, errors.New("run validation: fixed program output exceeded its bound")
	}
	if waitErr != nil {
		if runContext.Err() != nil {
			return receipt, errors.New("run validation: fixed program was cancelled or timed out")
		}
		return receipt, errors.New("run validation: fixed program failed")
	}
	return receipt, nil
}

// Stop terminates only the exact operation-owned validation process group.
func (runner *Runner) Stop(ctx context.Context, operationID, taskHandle string) error {
	if runner == nil || ctx == nil {
		return errors.New("stop validation: runner and context are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runner.mu.Lock()
	active, exists := runner.active[operationID]
	runner.mu.Unlock()
	if !exists || active.taskHandle != taskHandle || active.command.Process == nil {
		return errors.New("stop validation: owning operation is not running")
	}
	if err := terminateProcessGroup(active.command.Process.Pid); err != nil {
		return errors.New("stop validation: process group identity is unavailable")
	}
	return nil
}

func (runner *Runner) register(operationID, taskHandle string, command *exec.Cmd) error {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if _, exists := runner.active[operationID]; exists {
		return errors.New("run validation: operation is already active")
	}
	runner.active[operationID] = activeProcess{taskHandle: taskHandle, command: command}
	return nil
}

func (runner *Runner) unregister(operationID string) {
	runner.mu.Lock()
	delete(runner.active, operationID)
	runner.mu.Unlock()
}

type boundedHashWriter struct {
	hash     hash.Hash
	limit    int64
	bytes    int64
	exceeded bool
}

func newBoundedHashWriter(limit int64) *boundedHashWriter {
	return &boundedHashWriter{hash: sha256.New(), limit: limit}
}

func (writer *boundedHashWriter) Write(contents []byte) (int, error) {
	remaining := writer.limit - writer.bytes
	accepted := len(contents)
	if int64(accepted) > remaining {
		accepted = max(0, int(remaining))
		writer.exceeded = true
	}
	if accepted > 0 {
		_, _ = writer.hash.Write(contents[:accepted])
		writer.bytes += int64(accepted)
	}
	return len(contents), nil
}

func (writer *boundedHashWriter) sum() string {
	return fmt.Sprintf("%x", writer.hash.Sum(nil))
}

func processStartIdentity(operationID string, pid int, startedAt time.Time) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(operationID+"\x00"+strconv.Itoa(pid)+"\x00"+startedAt.Format(time.RFC3339Nano))))
}

func processGroupAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessGroup(pid int) error {
	if pid < 1 {
		return errors.New("invalid process group")
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}

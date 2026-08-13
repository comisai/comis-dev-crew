package livecampaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const maxManifestBytes = 64 * 1024

var (
	safeReferencePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/-]{0,255}$`)
	checkpointMarkerPattern = regexp.MustCompile(`^e0cp-[a-z0-9][a-z0-9-]{5,95}$`)
	githubRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	secretNamePattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,255}$`)
	serviceUnitPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}\.service$`)
)

var requiredCheckpointKinds = []string{
	"task_request",
	"unrelated_conversation",
	"mcp_restarted_ack",
	"decision_reply",
	"pause_handback",
	"reconcile_approval",
	"devcrew_restart_ready",
	"devcrew_restarted_ack",
	"comis_restart_ready",
	"comis_restarted_ack",
	"cleanup_confirmation",
}

// Manifest is the strict, content-free authority for one protected E0 campaign.
type Manifest struct {
	SchemaVersion int                    `json:"schemaVersion"`
	CampaignID    string                 `json:"campaignId"`
	StartedAtMs   int64                  `json:"startedAtMs"`
	EndedAtMs     int64                  `json:"endedAtMs"`
	DevCrew       DevCrewTarget          `json:"devcrew"`
	Comis         ComisTarget            `json:"comis"`
	Telegram      TelegramTarget         `json:"telegram"`
	GitHub        GitHubTarget           `json:"github"`
	Services      ServiceTargets         `json:"services"`
	Tasks         []TaskExpectation      `json:"tasks"`
	Operations    []OperationExpectation `json:"operations"`
}

type DevCrewTarget struct {
	CLIPath      string `json:"cliPath"`
	SocketPath   string `json:"socketPath"`
	RepositoryID string `json:"repositoryId"`
}

type ComisTarget struct {
	NodePath              string   `json:"nodePath"`
	CLIScriptPath         string   `json:"cliScriptPath"`
	CodeRoot              string   `json:"codeRoot"`
	DataDir               string   `json:"dataDir"`
	AgentID               string   `json:"agentId"`
	SecretResidencyScript string   `json:"secretResidencyScript"`
	SecretNames           []string `json:"secretNames"`
	ExplainRefs           []string `json:"explainRefs"`
}

type TelegramTarget struct {
	OriginChatID string               `json:"originChatId"`
	NewerChatID  string               `json:"newerChatId"`
	SenderID     string               `json:"senderId"`
	Checkpoints  []TelegramCheckpoint `json:"checkpoints"`
}

type TelegramCheckpoint struct {
	Kind   string `json:"kind"`
	ChatID string `json:"chatId"`
	Marker string `json:"marker"`
}

type GitHubTarget struct {
	CLIPath         string   `json:"cliPath"`
	GitPath         string   `json:"gitPath"`
	Repository      string   `json:"repository"`
	PrimaryCheckout string   `json:"primaryCheckout"`
	BaseBranch      string   `json:"baseBranch"`
	RequiredChecks  []string `json:"requiredChecks"`
}

type ServiceTargets struct {
	SystemctlPath  string `json:"systemctlPath"`
	IsolationLabel string `json:"isolationLabel"`
	MCPUnit        string `json:"mcpUnit"`
	DevCrewUnit    string `json:"devcrewUnit"`
	ComisUnit      string `json:"comisUnit"`
}

type TaskExpectation struct {
	TaskHandle           string `json:"taskHandle"`
	WorkerProfileID      string `json:"workerProfileId"`
	ManagedRunID         string `json:"managedRunId"`
	ExpectReconciliation bool   `json:"expectReconciliation"`
}

type OperationExpectation struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
	Command     string `json:"command"`
}

// LoadManifest decodes and validates one protected campaign manifest.
func LoadManifest(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect live campaign manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Manifest{}, errors.New("live campaign manifest must be one regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Manifest{}, errors.New("live campaign manifest must not be readable or writable by group or other users")
	}
	if info.Size() <= 0 || info.Size() > maxManifestBytes {
		return Manifest{}, fmt.Errorf("live campaign manifest must contain 1..%d bytes", maxManifestBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open live campaign manifest: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode strict live campaign manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("decode strict live campaign manifest: trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("decode strict live campaign manifest trailing data: %w", err)
	}
	if err := manifest.validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (manifest Manifest) validate() error {
	if manifest.SchemaVersion != 1 {
		return errors.New("live campaign schemaVersion must equal 1")
	}
	if !safeReferencePattern.MatchString(manifest.CampaignID) {
		return errors.New("campaignId must be one bounded opaque identifier")
	}
	const minimumCampaignMs = int64(60 * 60 * 1000)
	const maximumCampaignMs = int64(24 * 60 * 60 * 1000)
	durationMs := manifest.EndedAtMs - manifest.StartedAtMs
	if manifest.StartedAtMs <= 0 || durationMs < minimumCampaignMs || durationMs > maximumCampaignMs {
		return errors.New("campaign time window must be closed and span between one and twenty-four hours")
	}
	for name, path := range map[string]string{
		"devcrew.cliPath":             manifest.DevCrew.CLIPath,
		"devcrew.socketPath":          manifest.DevCrew.SocketPath,
		"comis.nodePath":              manifest.Comis.NodePath,
		"comis.cliScriptPath":         manifest.Comis.CLIScriptPath,
		"comis.codeRoot":              manifest.Comis.CodeRoot,
		"comis.dataDir":               manifest.Comis.DataDir,
		"comis.secretResidencyScript": manifest.Comis.SecretResidencyScript,
		"github.cliPath":              manifest.GitHub.CLIPath,
		"github.gitPath":              manifest.GitHub.GitPath,
		"github.primaryCheckout":      manifest.GitHub.PrimaryCheckout,
		"services.systemctlPath":      manifest.Services.SystemctlPath,
	} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s must be one clean absolute path", name)
		}
	}
	for name, value := range map[string]string{
		"devcrew.repositoryId":  manifest.DevCrew.RepositoryID,
		"comis.agentId":         manifest.Comis.AgentID,
		"telegram.originChatId": manifest.Telegram.OriginChatID,
		"telegram.newerChatId":  manifest.Telegram.NewerChatID,
		"telegram.senderId":     manifest.Telegram.SenderID,
		"github.baseBranch":     manifest.GitHub.BaseBranch,
	} {
		if !safeReferencePattern.MatchString(value) {
			return fmt.Errorf("%s must be one bounded opaque identifier", name)
		}
	}
	if manifest.Telegram.OriginChatID == manifest.Telegram.NewerChatID {
		return errors.New("origin and newer Telegram conversations must be distinct")
	}
	if !githubRepositoryPattern.MatchString(manifest.GitHub.Repository) {
		return errors.New("github.repository must be one owner/name identifier")
	}
	if err := validateUniqueStrings("Comis secret names", manifest.Comis.SecretNames, secretNamePattern.MatchString); err != nil {
		return err
	}
	if err := validateUniqueStrings("Comis explanation references", manifest.Comis.ExplainRefs, safeReferencePattern.MatchString); err != nil {
		return err
	}
	if err := validateUniqueStrings("GitHub required checks", manifest.GitHub.RequiredChecks, validDisplayName); err != nil {
		return err
	}
	if err := manifest.validateCheckpoints(); err != nil {
		return err
	}
	if err := manifest.validateServices(); err != nil {
		return err
	}
	return manifest.validateTasksAndOperations()
}

func validDisplayName(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validateUniqueStrings(name string, values []string, valid func(string) bool) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !valid(value) {
			return fmt.Errorf("%s contains an invalid value", name)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%s must be unique", name)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (manifest Manifest) validateCheckpoints() error {
	if len(manifest.Telegram.Checkpoints) != len(requiredCheckpointKinds) {
		for _, kind := range requiredCheckpointKinds {
			if !hasCheckpoint(manifest.Telegram.Checkpoints, kind) {
				return fmt.Errorf("telegram checkpoint %s is required", kind)
			}
		}
		return errors.New("telegram checkpoints must contain exactly the closed required catalog")
	}
	seenKinds := make(map[string]struct{}, len(requiredCheckpointKinds))
	seenMarkers := make(map[string]struct{}, len(requiredCheckpointKinds))
	for _, checkpoint := range manifest.Telegram.Checkpoints {
		if !contains(requiredCheckpointKinds, checkpoint.Kind) {
			return fmt.Errorf("unknown telegram checkpoint kind %s", checkpoint.Kind)
		}
		if _, exists := seenKinds[checkpoint.Kind]; exists {
			return fmt.Errorf("telegram checkpoint kind %s is duplicated", checkpoint.Kind)
		}
		seenKinds[checkpoint.Kind] = struct{}{}
		if !checkpointMarkerPattern.MatchString(checkpoint.Marker) {
			return fmt.Errorf("telegram checkpoint %s marker must be one bounded e0cp identifier", checkpoint.Kind)
		}
		if _, exists := seenMarkers[checkpoint.Marker]; exists {
			return errors.New("telegram checkpoint markers must be unique")
		}
		seenMarkers[checkpoint.Marker] = struct{}{}
		expectedChat := manifest.Telegram.OriginChatID
		if checkpoint.Kind == "unrelated_conversation" {
			expectedChat = manifest.Telegram.NewerChatID
		}
		if checkpoint.ChatID != expectedChat {
			return fmt.Errorf("telegram checkpoint %s is bound to the wrong conversation", checkpoint.Kind)
		}
	}
	for _, kind := range requiredCheckpointKinds {
		if _, exists := seenKinds[kind]; !exists {
			return fmt.Errorf("telegram checkpoint %s is required", kind)
		}
	}
	return nil
}

func (manifest Manifest) validateServices() error {
	if !safeReferencePattern.MatchString(manifest.Services.IsolationLabel) || strings.Contains(manifest.Services.IsolationLabel, "/") {
		return errors.New("service isolation label must be one bounded identifier")
	}
	units := []string{manifest.Services.MCPUnit, manifest.Services.DevCrewUnit, manifest.Services.ComisUnit}
	seen := make(map[string]struct{}, len(units))
	for _, unit := range units {
		if !serviceUnitPattern.MatchString(unit) {
			return errors.New("service unit names must be bounded systemd service identifiers")
		}
		if !strings.Contains(unit, manifest.Services.IsolationLabel) {
			return fmt.Errorf("service unit %s does not contain the isolation label", unit)
		}
		if _, exists := seen[unit]; exists {
			return errors.New("campaign service units must be distinct")
		}
		seen[unit] = struct{}{}
	}
	return nil
}

func (manifest Manifest) validateTasksAndOperations() error {
	if len(manifest.Tasks) != 2 {
		return errors.New("live campaign requires exactly two ship-task lanes")
	}
	tasks := make(map[string]TaskExpectation, len(manifest.Tasks))
	profiles := make(map[string]struct{}, len(manifest.Tasks))
	runs := make(map[string]struct{}, len(manifest.Tasks))
	recoveredTasks := 0
	for _, task := range manifest.Tasks {
		if err := domain.ValidateTaskHandle(task.TaskHandle); err != nil {
			return fmt.Errorf("invalid task handle: %w", err)
		}
		if _, exists := tasks[task.TaskHandle]; exists {
			return errors.New("task handles must be distinct")
		}
		if !safeReferencePattern.MatchString(task.WorkerProfileID) {
			return errors.New("worker profile IDs must be bounded identifiers")
		}
		if _, exists := profiles[task.WorkerProfileID]; exists {
			return errors.New("worker profiles must be distinct")
		}
		if !safeReferencePattern.MatchString(task.ManagedRunID) {
			return errors.New("managed run IDs must be bounded identifiers")
		}
		if _, exists := runs[task.ManagedRunID]; exists {
			return errors.New("managed run IDs must be distinct")
		}
		tasks[task.TaskHandle] = task
		profiles[task.WorkerProfileID] = struct{}{}
		runs[task.ManagedRunID] = struct{}{}
		if task.ExpectReconciliation {
			recoveredTasks++
		}
	}
	if recoveredTasks != 1 {
		return errors.New("exactly one task must require clean-candidate reconciliation")
	}
	operationIDs := make(map[string]struct{}, len(manifest.Operations))
	commandsByTask := make(map[string]map[string]int, len(tasks))
	for _, operation := range manifest.Operations {
		if err := domain.ValidateOperationID(operation.OperationID); err != nil {
			return fmt.Errorf("invalid operation ID: %w", err)
		}
		if _, exists := operationIDs[operation.OperationID]; exists {
			return errors.New("operation IDs must be distinct")
		}
		operationIDs[operation.OperationID] = struct{}{}
		if _, exists := tasks[operation.TaskHandle]; !exists {
			return errors.New("operation task handle must select one manifest task")
		}
		if operation.Command != "ReconcileTask" && operation.Command != "HandbackTask" && operation.Command != "CleanupTask" {
			return fmt.Errorf("operation command %s is outside the closed live catalog", operation.Command)
		}
		if commandsByTask[operation.TaskHandle] == nil {
			commandsByTask[operation.TaskHandle] = make(map[string]int)
		}
		commandsByTask[operation.TaskHandle][operation.Command]++
	}
	for handle, task := range tasks {
		if commandsByTask[handle]["CleanupTask"] != 1 {
			return fmt.Errorf("task %s requires exactly one CleanupTask operation", handle)
		}
		if task.ExpectReconciliation && commandsByTask[handle]["ReconcileTask"] != 1 {
			return fmt.Errorf("recovered task %s requires exactly one ReconcileTask operation", handle)
		}
		if !task.ExpectReconciliation && commandsByTask[handle]["HandbackTask"] != 1 {
			return fmt.Errorf("non-recovered task %s requires exactly one HandbackTask operation", handle)
		}
	}
	return nil
}

func hasCheckpoint(checkpoints []TelegramCheckpoint, kind string) bool {
	for _, checkpoint := range checkpoints {
		if checkpoint.Kind == kind {
			return true
		}
	}
	return false
}

func contains(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

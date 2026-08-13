package livecampaign

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const maximumCommandOutputBytes = 4 << 20

type Command struct {
	Path string
	Args []string
	Env  map[string]string
}

type Executor interface {
	Run(context.Context, Command) ([]byte, error)
}

type EvidenceCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Verdict struct {
	SchemaVersion     int             `json:"schemaVersion"`
	CampaignID        string          `json:"campaignId"`
	CapturedAtMs      int64           `json:"capturedAtMs"`
	Passed            bool            `json:"passed"`
	EvidenceDirectory string          `json:"evidenceDirectory"`
	Checks            []EvidenceCheck `json:"checks"`
}

type collector struct {
	ctx       context.Context
	manifest  Manifest
	executor  Executor
	directory string
	artifacts []string
	checks    []EvidenceCheck
}

type gitTruth struct {
	Repository      string `json:"repository"`
	PrimaryCheckout string `json:"primaryCheckout"`
	BaseBranch      string `json:"baseBranch"`
	BaseRevision    string `json:"baseRevision"`
	PrimaryClean    bool   `json:"primaryClean"`
}

type pullRequestEvidence struct {
	TaskHandle  string           `json:"taskHandle"`
	PullRequest GitHubPull       `json:"pullRequest"`
	Checks      []GitHubCheckRun `json:"checks"`
}

type residencyReport struct {
	SchemaVersion int      `json:"schemaVersion"`
	ScannedFiles  int      `json:"scannedFiles"`
	ReadErrors    []string `json:"readErrors"`
	TotalMatches  int      `json:"totalMatches"`
	Secrets       map[string]struct {
		Retrieved    bool `json:"retrieved"`
		TotalMatches int  `json:"totalMatches"`
	} `json:"secrets"`
}

type artifactHash struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

func Collect(ctx context.Context, manifest Manifest, outputRoot string, executor Executor, capturedAtMs int64) (Verdict, error) {
	if ctx == nil || executor == nil || capturedAtMs <= 0 {
		return Verdict{}, errors.New("collect live closeout: context, executor, and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return Verdict{}, fmt.Errorf("collect live closeout: %w", err)
	}
	if !filepath.IsAbs(outputRoot) || filepath.Clean(outputRoot) != outputRoot {
		return Verdict{}, errors.New("collect live closeout: evidence root must be one clean absolute path")
	}
	if err := ensurePrivateDirectory(outputRoot); err != nil {
		return Verdict{}, err
	}
	directory := filepath.Join(outputRoot, manifest.CampaignID)
	if _, err := os.Lstat(directory); err == nil {
		return Verdict{}, errors.New("collect live closeout: campaign evidence directory already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Verdict{}, fmt.Errorf("collect live closeout: inspect campaign evidence directory: %w", err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return Verdict{}, fmt.Errorf("collect live closeout: create campaign evidence directory: %w", err)
	}
	instance := &collector{ctx: ctx, manifest: manifest, executor: executor, directory: directory}
	if err := instance.writeJSON("manifest.json", manifest); err != nil {
		return Verdict{}, err
	}
	if err := instance.collectDevCrew(); err != nil {
		return instance.failureVerdict(capturedAtMs, err)
	}
	if err := instance.collectTelegramAndComis(); err != nil {
		return instance.failureVerdict(capturedAtMs, err)
	}
	if err := instance.collectGitAndForge(); err != nil {
		return instance.failureVerdict(capturedAtMs, err)
	}
	if err := instance.collectSecretPosture(); err != nil {
		return instance.failureVerdict(capturedAtMs, err)
	}
	if err := instance.writeHashes(); err != nil {
		return instance.failureVerdict(capturedAtMs, err)
	}
	verdict := Verdict{
		SchemaVersion: 1, CampaignID: manifest.CampaignID, CapturedAtMs: capturedAtMs,
		Passed: true, EvidenceDirectory: directory, Checks: instance.checks,
	}
	if err := instance.writeJSON("verdict.json", verdict); err != nil {
		return Verdict{}, err
	}
	return verdict, nil
}

func (instance *collector) collectDevCrew() error {
	base := []string{"--socket", instance.manifest.DevCrew.SocketPath}
	status, err := instance.run(Command{Path: instance.manifest.DevCrew.CLIPath, Args: appendArgs(base, "service", "status")})
	if err != nil {
		return fmt.Errorf("collect live closeout: DevCrew service status unavailable: %w", err)
	}
	if err := instance.writeBytes("devcrew-service-status.txt", status); err != nil {
		return err
	}
	var doctor application.DiagnosticReport
	if err := instance.runJSON(Command{Path: instance.manifest.DevCrew.CLIPath, Args: appendArgs(base, "doctor", "--format", "json")}, &doctor); err != nil {
		return fmt.Errorf("collect live closeout: DevCrew doctor unavailable: %w", err)
	}
	if doctor.SchemaVersion != 1 || doctor.Completeness != application.CompletenessComplete ||
		doctor.ServiceHealth != application.HealthHealthy || doctor.ComisHealth != application.HealthHealthy {
		return errors.New("collect live closeout: DevCrew service or Comis dependency is not healthy and complete")
	}
	if err := instance.writeJSON("devcrew-doctor.json", doctor); err != nil {
		return err
	}
	var fleet application.FleetSnapshot
	if err := instance.runJSON(Command{Path: instance.manifest.DevCrew.CLIPath, Args: appendArgs(base, "status", "--format", "json")}, &fleet); err != nil {
		return fmt.Errorf("collect live closeout: DevCrew fleet unavailable: %w", err)
	}
	if fleet.SchemaVersion != 1 || fleet.Completeness != application.CompletenessComplete ||
		fleet.ServiceHealth != application.HealthHealthy || fleet.ComisHealth != application.HealthHealthy || len(fleet.Tasks) != len(instance.manifest.Tasks) {
		return errors.New("collect live closeout: isolated DevCrew fleet is incomplete, degraded, or contains unexpected tasks")
	}
	if err := instance.writeJSON("devcrew-fleet.json", fleet); err != nil {
		return err
	}
	details := make([]application.TaskDetail, 0, len(instance.manifest.Tasks))
	seenHeads := make(map[string]struct{}, len(instance.manifest.Tasks))
	seenPulls := make(map[string]struct{}, len(instance.manifest.Tasks))
	for _, expectation := range instance.manifest.Tasks {
		var detail application.TaskDetail
		if err := instance.runJSON(Command{Path: instance.manifest.DevCrew.CLIPath, Args: appendArgs(base, "task", "show", expectation.TaskHandle, "--format", "json")}, &detail); err != nil {
			return fmt.Errorf("collect live closeout: task %s detail unavailable: %w", expectation.TaskHandle, err)
		}
		if err := VerifyTask(instance.manifest, expectation, detail); err != nil {
			return fmt.Errorf("collect live closeout: task %s evidence refused: %w", expectation.TaskHandle, err)
		}
		if _, exists := seenHeads[detail.Evidence.Candidate.HeadRevision]; exists {
			return errors.New("collect live closeout: task candidate heads are not isolated")
		}
		seenHeads[detail.Evidence.Candidate.HeadRevision] = struct{}{}
		if _, exists := seenPulls[detail.Evidence.Delivery.PullRequestID]; exists {
			return errors.New("collect live closeout: task pull-request identities are not isolated")
		}
		seenPulls[detail.Evidence.Delivery.PullRequestID] = struct{}{}
		var explanation application.TaskExplanation
		if err := instance.runJSON(Command{Path: instance.manifest.DevCrew.CLIPath, Args: appendArgs(base, "task", "explain", expectation.TaskHandle, "--format", "json")}, &explanation); err != nil {
			return fmt.Errorf("collect live closeout: task %s explanation unavailable: %w", expectation.TaskHandle, err)
		}
		if explanation.SchemaVersion != 1 || explanation.Completeness != application.CompletenessComplete ||
			!reflect.DeepEqual(explanation.Summary, detail.Summary) || !reflect.DeepEqual(explanation.Evidence, detail.Evidence) {
			return fmt.Errorf("collect live closeout: task %s explanation differs from task evidence", expectation.TaskHandle)
		}
		if err := instance.writeJSON("devcrew-task-"+expectation.TaskHandle+".json", detail); err != nil {
			return err
		}
		if err := instance.writeJSON("devcrew-explain-"+expectation.TaskHandle+".json", explanation); err != nil {
			return err
		}
		details = append(details, detail)
	}
	for _, expectation := range instance.manifest.Operations {
		var view application.OperationView
		if err := instance.runJSON(Command{Path: instance.manifest.DevCrew.CLIPath, Args: appendArgs(base, "task", "operation", expectation.OperationID, "--format", "json")}, &view); err != nil {
			return fmt.Errorf("collect live closeout: operation %s unavailable: %w", expectation.OperationID, err)
		}
		if err := VerifyOperation(expectation, view); err != nil {
			return fmt.Errorf("collect live closeout: operation %s evidence refused: %w", expectation.OperationID, err)
		}
		if err := instance.writeJSON("devcrew-operation-"+expectation.OperationID+".json", view); err != nil {
			return err
		}
	}
	if err := instance.writeJSON("devcrew-tasks.json", details); err != nil {
		return err
	}
	instance.pass("devcrew_service_and_fleet")
	instance.pass("task_candidate_validation_delivery_cleanup")
	instance.pass("operation_reconciliation")
	return nil
}

func (instance *collector) collectTelegramAndComis() error {
	comisEnv := instance.comisEnv()
	messageArgs := []string{
		instance.manifest.Comis.CLIScriptPath, "messages", "--channel", "telegram",
		"--sender", instance.manifest.Telegram.SenderID, "--agent", instance.manifest.Comis.AgentID,
		"--since", strconv.FormatInt(instance.manifest.StartedAtMs, 10),
		"--until", strconv.FormatInt(instance.manifest.EndedAtMs+1, 10),
		"--limit", "10000", "--format", "json",
	}
	var report MessageReport
	if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: messageArgs, Env: comisEnv}, &report); err != nil {
		return fmt.Errorf("collect live closeout: Telegram messages unavailable: %w", err)
	}
	checkpoints, err := VerifyMessages(instance.manifest, report)
	if err != nil {
		return fmt.Errorf("collect live closeout: Telegram evidence refused: %w", err)
	}
	if err := instance.writeJSON("telegram-checkpoints.json", checkpoints); err != nil {
		return err
	}
	durationHours := (instance.manifest.EndedAtMs - instance.manifest.StartedAtMs + 3_599_999) / 3_600_000
	if durationHours < 1 {
		durationHours = 1
	}
	var health json.RawMessage
	if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: []string{
		instance.manifest.Comis.CLIScriptPath, "system-health", "--since", strconv.FormatInt(durationHours, 10),
		"--format", "json", "--offline",
	}, Env: comisEnv}, &health); err != nil {
		return fmt.Errorf("collect live closeout: Comis system health unavailable: %w", err)
	}
	if err := instance.writeRawJSON("comis-system-health.json", health); err != nil {
		return err
	}
	for index, reference := range instance.manifest.Comis.ExplainRefs {
		var explanation json.RawMessage
		if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: []string{
			instance.manifest.Comis.CLIScriptPath, "explain", reference, "--format", "json", "--depth", "full", "--offline",
		}, Env: comisEnv}, &explanation); err != nil {
			return fmt.Errorf("collect live closeout: Comis explanation %d unavailable: %w", index+1, err)
		}
		if err := instance.writeRawJSON(fmt.Sprintf("comis-explain-%02d.json", index+1), explanation); err != nil {
			return err
		}
	}
	instance.pass("real_human_telegram_checkpoints")
	instance.pass("comis_observability")
	return nil
}

func (instance *collector) collectGitAndForge() error {
	status, err := instance.run(Command{Path: instance.manifest.GitHub.GitPath, Args: []string{
		"-C", instance.manifest.GitHub.PrimaryCheckout, "status", "--porcelain=v1",
	}})
	if err != nil {
		return fmt.Errorf("collect live closeout: Git primary status unavailable: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		return errors.New("collect live closeout: canonical primary checkout is dirty")
	}
	base, err := instance.run(Command{Path: instance.manifest.GitHub.GitPath, Args: []string{
		"-C", instance.manifest.GitHub.PrimaryCheckout, "rev-parse", instance.manifest.GitHub.BaseBranch,
	}})
	if err != nil {
		return fmt.Errorf("collect live closeout: Git base revision unavailable: %w", err)
	}
	baseRevision := strings.TrimSpace(string(base))
	if !revisionPattern.MatchString(baseRevision) {
		return errors.New("collect live closeout: Git base revision is invalid")
	}
	gitEvidence := gitTruth{
		Repository: instance.manifest.GitHub.Repository, PrimaryCheckout: instance.manifest.GitHub.PrimaryCheckout,
		BaseBranch: instance.manifest.GitHub.BaseBranch, BaseRevision: baseRevision, PrimaryClean: true,
	}
	if err := instance.writeJSON("git-truth.json", gitEvidence); err != nil {
		return err
	}
	pulls := make([]pullRequestEvidence, 0, len(instance.manifest.Tasks))
	seenBranches := make(map[string]struct{}, len(instance.manifest.Tasks))
	for _, expectation := range instance.manifest.Tasks {
		var detail application.TaskDetail
		if err := instance.readArtifactJSON("devcrew-task-"+expectation.TaskHandle+".json", &detail); err != nil {
			return err
		}
		if detail.BaseRevision != baseRevision {
			return fmt.Errorf("collect live closeout: task %s base differs from current canonical Git base", expectation.TaskHandle)
		}
		number := strings.TrimPrefix(detail.Evidence.Delivery.PullRequestID, "github-pr-")
		var pull GitHubPull
		if err := instance.runJSON(Command{Path: instance.manifest.GitHub.CLIPath, Args: []string{
			"api", "repos/" + instance.manifest.GitHub.Repository + "/pulls/" + number,
		}}, &pull); err != nil {
			return fmt.Errorf("collect live closeout: GitHub pull request %s unavailable: %w", number, err)
		}
		var checks GitHubChecks
		if err := instance.runJSON(Command{Path: instance.manifest.GitHub.CLIPath, Args: []string{
			"api", "-H", "Accept: application/vnd.github+json",
			"repos/" + instance.manifest.GitHub.Repository + "/commits/" + detail.Evidence.Candidate.HeadRevision + "/check-runs",
		}}, &checks); err != nil {
			return fmt.Errorf("collect live closeout: GitHub checks for task %s unavailable: %w", expectation.TaskHandle, err)
		}
		if err := VerifyPullRequest(instance.manifest.GitHub, detail, pull, checks); err != nil {
			return fmt.Errorf("collect live closeout: GitHub truth for task %s refused: %w", expectation.TaskHandle, err)
		}
		if _, exists := seenBranches[pull.Head.Ref]; exists {
			return errors.New("collect live closeout: pull-request branches are not isolated")
		}
		seenBranches[pull.Head.Ref] = struct{}{}
		pulls = append(pulls, pullRequestEvidence{TaskHandle: expectation.TaskHandle, PullRequest: pull, Checks: checks.Runs})
	}
	if err := instance.writeJSON("github-truth.json", pulls); err != nil {
		return err
	}
	instance.pass("git_and_github_truth")
	return nil
}

func (instance *collector) collectSecretPosture() error {
	comisEnv := instance.comisEnv()
	var audit []json.RawMessage
	if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: []string{
		instance.manifest.Comis.CLIScriptPath, "secrets", "audit", "--check", "--json",
	}, Env: comisEnv}, &audit); err != nil {
		return fmt.Errorf("collect live closeout: Comis plaintext-secret audit unavailable: %w", err)
	}
	if len(audit) != 0 {
		return errors.New("collect live closeout: Comis plaintext-secret audit found residency")
	}
	if err := instance.writeJSON("comis-secrets-audit.json", audit); err != nil {
		return err
	}
	args := append([]string{instance.manifest.Comis.SecretResidencyScript}, instance.manifest.Comis.SecretNames...)
	residencyEnv := instance.comisEnv()
	residencyEnv["RIG_MODE"] = "local"
	residencyEnv["COMIS_SRC"] = instance.manifest.Comis.CodeRoot
	var residency residencyReport
	if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: args, Env: residencyEnv}, &residency); err != nil {
		return fmt.Errorf("collect live closeout: count-only secret residency oracle unavailable: %w", err)
	}
	if residency.SchemaVersion != 1 || residency.ScannedFiles <= 0 || len(residency.ReadErrors) != 0 ||
		residency.TotalMatches != 0 || len(residency.Secrets) != len(instance.manifest.Comis.SecretNames) {
		return errors.New("collect live closeout: count-only secret residency oracle is incomplete or found plaintext")
	}
	for _, name := range instance.manifest.Comis.SecretNames {
		result, exists := residency.Secrets[name]
		if !exists || !result.Retrieved || result.TotalMatches != 0 {
			return fmt.Errorf("collect live closeout: secret residency result for %s is incomplete", name)
		}
	}
	if err := instance.writeJSON("secret-residency.json", residency); err != nil {
		return err
	}
	instance.pass("secret_residency")
	return nil
}

func (instance *collector) comisEnv() map[string]string {
	return map[string]string{
		"COMIS_DATA_DIR":     instance.manifest.Comis.DataDir,
		"COMIS_CONFIG_PATHS": filepath.Join(instance.manifest.Comis.DataDir, "config.yaml"),
	}
}

func (instance *collector) pass(name string) {
	instance.checks = append(instance.checks, EvidenceCheck{Name: name, Status: "pass"})
}

func (instance *collector) failureVerdict(capturedAtMs int64, cause error) (Verdict, error) {
	verdict := Verdict{
		SchemaVersion: 1, CampaignID: instance.manifest.CampaignID, CapturedAtMs: capturedAtMs,
		Passed: false, EvidenceDirectory: instance.directory, Checks: instance.checks,
	}
	_ = instance.writeJSON("verdict.json", verdict)
	return verdict, cause
}

func (instance *collector) run(command Command) ([]byte, error) {
	output, err := instance.executor.Run(instance.ctx, command)
	if err != nil {
		return nil, err
	}
	if len(output) > maximumCommandOutputBytes {
		return nil, errors.New("command output exceeded the bounded live evidence limit")
	}
	return output, nil
}

func (instance *collector) runJSON(command Command, destination any) error {
	output, err := instance.run(command)
	if err != nil {
		return err
	}
	if len(output) == 0 || len(output) > maximumCommandOutputBytes {
		return errors.New("JSON command output is empty or oversized")
	}
	if err := json.Unmarshal(output, destination); err != nil {
		return errors.New("JSON command output is malformed")
	}
	if raw, ok := destination.(*json.RawMessage); ok {
		trimmed := strings.TrimSpace(string(*raw))
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			return errors.New("JSON command output is not one report object or array")
		}
	}
	return nil
}

func (instance *collector) writeJSON(name string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("collect live closeout: encode artifact %s: %w", name, err)
	}
	contents = append(contents, '\n')
	return instance.writeBytes(name, contents)
}

func (instance *collector) writeRawJSON(name string, value json.RawMessage) error {
	var normalized any
	if err := json.Unmarshal(value, &normalized); err != nil {
		return fmt.Errorf("collect live closeout: normalize artifact %s: %w", name, err)
	}
	return instance.writeJSON(name, normalized)
}

func (instance *collector) writeBytes(name string, contents []byte) error {
	if filepath.Base(name) != name || name == "." || len(contents) > maximumCommandOutputBytes {
		return errors.New("collect live closeout: artifact name or size is invalid")
	}
	path := filepath.Join(instance.directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("collect live closeout: write artifact %s: %w", name, err)
	}
	instance.artifacts = append(instance.artifacts, name)
	return nil
}

func (instance *collector) readArtifactJSON(name string, destination any) error {
	contents, err := os.ReadFile(filepath.Join(instance.directory, name))
	if err != nil {
		return fmt.Errorf("collect live closeout: read artifact %s: %w", name, err)
	}
	if err := json.Unmarshal(contents, destination); err != nil {
		return fmt.Errorf("collect live closeout: parse artifact %s: %w", name, err)
	}
	return nil
}

func (instance *collector) writeHashes() error {
	names := append([]string(nil), instance.artifacts...)
	sort.Strings(names)
	hashes := make([]artifactHash, 0, len(names))
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(instance.directory, name))
		if err != nil {
			return fmt.Errorf("collect live closeout: hash artifact %s: %w", name, err)
		}
		digest := sha256.Sum256(contents)
		hashes = append(hashes, artifactHash{File: name, SHA256: hex.EncodeToString(digest[:]), Bytes: len(contents)})
	}
	return instance.writeJSON("hashes.json", hashes)
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("collect live closeout: create evidence root: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("collect live closeout: inspect evidence root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("collect live closeout: evidence root must be one owner-private directory")
	}
	return nil
}

func appendArgs(prefix []string, suffix ...string) []string {
	result := make([]string, 0, len(prefix)+len(suffix))
	result = append(result, prefix...)
	return append(result, suffix...)
}

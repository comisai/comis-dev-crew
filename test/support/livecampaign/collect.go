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
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type collector struct {
	ctx       context.Context
	manifest  Manifest
	executor  Executor
	directory string
	artifacts []string
	checks    []EvidenceCheck
}

func Collect(
	ctx context.Context,
	manifest Manifest,
	outputRoot string,
	executor Executor,
	capturedAtMs int64,
	resources ResourceObservation,
	recovery RecoveryEvidence,
) (Verdict, error) {
	if ctx == nil || executor == nil || capturedAtMs <= 0 {
		return Verdict{}, errors.New("collect live closeout: context, executor, and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return Verdict{}, fmt.Errorf("collect live closeout: %w", err)
	}
	if err := manifest.requireResolvedOperationIdentities(); err != nil {
		return Verdict{}, fmt.Errorf("collect live closeout: %w", err)
	}
	if err := VerifyResourceObservation(manifest, resources); err != nil {
		return Verdict{}, fmt.Errorf("collect live closeout: resource observation refused: %w", err)
	}
	if err := VerifyRecoveryEvidence(manifest, recovery); err != nil {
		return Verdict{}, fmt.Errorf("collect live closeout: recovery evidence refused: %w", err)
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
	if err := instance.writeJSON("resource-observation.json", resources); err != nil {
		return Verdict{}, err
	}
	instance.pass("one_hour_resource_observation")
	if err := instance.writeJSON("recovery-evidence.json", recovery); err != nil {
		return Verdict{}, err
	}
	instance.pass("installation_upgrade_backup_restore_and_rollback")
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
	currentBaseRevision := strings.TrimSpace(string(base))
	if !revisionPattern.MatchString(currentBaseRevision) {
		return errors.New("collect live closeout: Git base revision is invalid")
	}
	pulls := make([]pullRequestEvidence, 0, len(instance.manifest.Tasks))
	seenBranches := make(map[string]struct{}, len(instance.manifest.Tasks))
	taskBaseRevision := ""
	for _, expectation := range instance.manifest.Tasks {
		var detail application.TaskDetail
		if err := instance.readArtifactJSON("devcrew-task-"+expectation.TaskHandle+".json", &detail); err != nil {
			return err
		}
		if taskBaseRevision == "" {
			taskBaseRevision = detail.BaseRevision
			if _, err := instance.run(Command{Path: instance.manifest.GitHub.GitPath, Args: []string{
				"-C", instance.manifest.GitHub.PrimaryCheckout, "merge-base", "--is-ancestor",
				taskBaseRevision, currentBaseRevision,
			}}); err != nil {
				return fmt.Errorf("collect live closeout: pinned task base is not an ancestor of the current canonical Git base: %w", err)
			}
		} else if detail.BaseRevision != taskBaseRevision {
			return errors.New("collect live closeout: task bases do not share one pinned revision")
		}
		number := strings.TrimPrefix(detail.Evidence.Delivery.PullRequestID, "github-pr-")
		var pull GitHubPull
		if err := instance.runJSON(Command{Path: instance.manifest.GitHub.CLIPath, UseGitHubToken: true, Args: []string{
			"api", "repos/" + instance.manifest.GitHub.Repository + "/pulls/" + number,
		}}, &pull); err != nil {
			return fmt.Errorf("collect live closeout: GitHub pull request %s unavailable: %w", number, err)
		}
		var checks GitHubChecks
		if err := instance.runJSON(Command{Path: instance.manifest.GitHub.CLIPath, UseGitHubToken: true, Args: []string{
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
	gitEvidence := gitTruth{
		Repository: instance.manifest.GitHub.Repository, PrimaryCheckout: instance.manifest.GitHub.PrimaryCheckout,
		BaseBranch: instance.manifest.GitHub.BaseBranch, TaskBaseRevision: taskBaseRevision,
		CurrentBaseRevision: currentBaseRevision, PrimaryClean: true,
	}
	if err := instance.writeJSON("git-truth.json", gitEvidence); err != nil {
		return err
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
	if err := instance.runJSON(Command{
		Path: instance.manifest.Comis.NodePath, Args: args, Env: residencyEnv,
		UseComisGatewayToken:   true,
		SecretEnvironmentNames: instance.manifest.Comis.SecretNames,
	}, &residency); err != nil {
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
	instance.record(name, evidenceStatusPass)
}

// record states an outcome for one named check. A campaign that cannot drive a claim
// records it as unclaimed; absence never becomes a pass.
func (instance *collector) record(name string, status string) {
	instance.checks = append(instance.checks, EvidenceCheck{Name: name, Status: status})
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

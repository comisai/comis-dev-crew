package livecampaign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maximumCommandArguments = 128
	maximumArgumentBytes    = 4096
)

type RealExecutor struct{}

func (RealExecutor) Run(ctx context.Context, command Command) ([]byte, error) {
	if ctx == nil || len(command.Args) > maximumCommandArguments {
		return nil, errors.New("execute protected command: context or argument count is invalid")
	}
	if err := validateExecutable(command.Path); err != nil {
		return nil, err
	}
	for _, argument := range command.Args {
		if len(argument) > maximumArgumentBytes || strings.ContainsRune(argument, 0) {
			return nil, errors.New("execute protected command: argument is invalid")
		}
	}
	for name, value := range command.Env {
		if !allowedCommandEnvironmentOverride(name) || strings.ContainsRune(value, 0) {
			return nil, errors.New("execute protected command: environment override is invalid")
		}
	}
	environment, err := protectedCommandEnvironment(
		command.Env, command.UseGitHubToken, command.UseComisGatewayToken,
	)
	if err != nil {
		return nil, err
	}
	process := exec.CommandContext(ctx, command.Path, command.Args...)
	process.Env = environment
	process.Stdin = nil
	process.Stderr = io.Discard
	stdout := &boundedBuffer{limit: maximumCommandOutputBytes}
	process.Stdout = stdout
	if err := process.Run(); err != nil {
		if stdout.exceeded {
			return nil, errors.New("execute protected command: stdout exceeded the output limit")
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return nil, fmt.Errorf("execute protected command: process exited with status %d", exitError.ExitCode())
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, errors.New("execute protected command: context ended before completion")
		}
		return nil, errors.New("execute protected command: process could not start or complete")
	}
	if stdout.exceeded {
		return nil, errors.New("execute protected command: stdout exceeded the output limit")
	}
	return stdout.Bytes(), nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(contents []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = true
		return len(contents), nil
	}
	if len(contents) > remaining {
		_, _ = buffer.buffer.Write(contents[:remaining])
		buffer.exceeded = true
		return len(contents), nil
	}
	return buffer.buffer.Write(contents)
}

func (buffer *boundedBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func ValidateRuntime(manifest Manifest) error {
	return validateRuntime(context.Background(), manifest, RealExecutor{})
}

func validateRuntime(ctx context.Context, manifest Manifest, executor Executor) error {
	if ctx == nil || executor == nil {
		return errors.New("validate protected runtime: context and executor are required")
	}
	if err := manifest.validate(); err != nil {
		return fmt.Errorf("validate protected runtime: %w", err)
	}
	for _, artifact := range manifest.Artifacts {
		if err := validatePinnedArtifact(artifact); err != nil {
			return fmt.Errorf("validate protected runtime: %s artifact: %w", artifact.Kind, err)
		}
		if err := validatePinnedArtifactVersion(ctx, manifest, artifact, executor); err != nil {
			return fmt.Errorf("validate protected runtime: %s artifact: %w", artifact.Kind, err)
		}
	}
	for _, artifact := range manifest.Recovery.PreviousArtifacts {
		if err := validatePinnedArtifact(artifact); err != nil {
			return fmt.Errorf("validate protected runtime: previous %s artifact: %w", artifact.Kind, err)
		}
		if err := validatePinnedArtifactVersion(ctx, manifest, artifact, executor); err != nil {
			return fmt.Errorf("validate protected runtime: previous %s artifact: %w", artifact.Kind, err)
		}
	}
	for _, worker := range manifest.Workers {
		if err := validatePinnedWorker(ctx, worker, executor); err != nil {
			return fmt.Errorf("validate protected runtime: %s worker: %w", worker.Kind, err)
		}
	}
	for name, path := range map[string]string{
		"DevCrew CLI": manifest.DevCrew.CLIPath,
		"Node":        manifest.Comis.NodePath,
		"GitHub CLI":  manifest.GitHub.CLIPath,
		"Git":         manifest.GitHub.GitPath,
		"systemctl":   manifest.Services.SystemctlPath,
	} {
		if err := validateExecutable(path); err != nil {
			return fmt.Errorf("validate protected runtime: %s: %w", name, err)
		}
	}
	for _, unit := range []struct {
		name   string
		digest string
	}{
		{name: manifest.Services.MCPUnit, digest: manifest.Services.MCPUnitSHA256},
		{name: manifest.Services.DevCrewUnit, digest: manifest.Services.DevCrewUnitSHA256},
		{name: manifest.Services.ComisUnit, digest: manifest.Services.ComisUnitSHA256},
	} {
		if err := validatePinnedServiceUnit(ctx, manifest.Services.SystemctlPath, unit.name, unit.digest, executor); err != nil {
			return fmt.Errorf("validate protected runtime: service unit %s: %w", unit.name, err)
		}
	}
	for _, target := range []struct {
		name string
		path string
		root string
	}{
		{name: "Comis CLI script", path: manifest.Comis.CLIScriptPath, root: manifest.Comis.CodeRoot},
		{name: "secret residency script", path: manifest.Comis.SecretResidencyScript, root: manifest.Comis.CodeRoot},
		{name: "DevCrew database", path: manifest.DevCrew.DatabasePath},
		{name: "Comis database", path: manifest.Comis.DatabasePath, root: manifest.Comis.DataDir},
		{name: "candidate configuration", path: manifest.Recovery.CandidateConfigPath},
	} {
		if err := validateCanonicalRegularFile(target.path); err != nil {
			return fmt.Errorf("validate protected runtime: %s: %w", target.name, err)
		}
		if target.root != "" && !pathWithin(target.root, target.path) {
			return fmt.Errorf("validate protected runtime: %s is outside its pinned root", target.name)
		}
		if target.name == "candidate configuration" {
			info, err := os.Lstat(target.path)
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				return errors.New("validate protected runtime: candidate configuration must be owner-private")
			}
		}
	}
	for name, path := range map[string]string{
		"DevCrew code root":     manifest.DevCrew.CodeRoot,
		"DevCrew worktree root": manifest.DevCrew.WorktreeRoot,
		"Comis code root":       manifest.Comis.CodeRoot,
		"Comis data root":       manifest.Comis.DataDir,
		"Git primary checkout":  manifest.GitHub.PrimaryCheckout,
	} {
		if err := validateCanonicalDirectory(path); err != nil {
			return fmt.Errorf("validate protected runtime: %s: %w", name, err)
		}
	}
	for _, source := range []struct {
		name   string
		root   string
		commit string
	}{
		{name: "Comis", root: manifest.Comis.CodeRoot, commit: manifest.Source.ComisCommit},
		{name: "DevCrew", root: manifest.DevCrew.CodeRoot, commit: manifest.Source.DevCrewCommit},
	} {
		if err := validatePinnedSource(ctx, manifest.GitHub.GitPath, source.root, source.commit, executor); err != nil {
			return fmt.Errorf("validate protected runtime: %s source: %w", source.name, err)
		}
	}
	dataInfo, err := os.Stat(manifest.Comis.DataDir)
	if err != nil || dataInfo.Mode().Perm()&0o077 != 0 {
		return errors.New("validate protected runtime: Comis data root must be owner-private")
	}
	socketInfo, err := os.Lstat(manifest.DevCrew.SocketPath)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 {
		return errors.New("validate protected runtime: DevCrew endpoint must be an existing Unix socket")
	}
	if socketInfo.Mode().Perm() != 0o600 {
		return errors.New("validate protected runtime: DevCrew Unix socket must have mode 0600")
	}
	if os.Getenv("GH_TOKEN") == "" {
		return errors.New("validate protected runtime: GH_TOKEN credential prerequisite is unavailable")
	}
	return nil
}

func validatePinnedSource(ctx context.Context, gitPath, root, commit string, executor Executor) error {
	output, err := executor.Run(ctx, Command{Path: gitPath, Args: []string{"-C", root, "rev-parse", "HEAD"}})
	if err != nil || strings.TrimSpace(string(output)) != commit {
		return errors.New("source HEAD differs from the protected manifest")
	}
	return nil
}

func validatePinnedWorker(ctx context.Context, pin WorkerPin, executor Executor) error {
	if err := validateExecutable(pin.Path); err != nil {
		return err
	}
	if err := validateCanonicalRegularFile(pin.Path); err != nil {
		return err
	}
	contents, err := os.ReadFile(pin.Path)
	if err != nil {
		return errors.New("read pinned worker executable")
	}
	if sha256Hex(contents) != pin.SHA256 {
		return errors.New("worker executable SHA-256 differs from the protected manifest")
	}
	output, err := executor.Run(ctx, Command{Path: pin.Path, Args: []string{"--version"}})
	if err != nil || strings.TrimSpace(string(output)) != pin.Version {
		return errors.New("reported worker version differs from the protected manifest")
	}
	return nil
}

func validatePinnedServiceUnit(
	ctx context.Context,
	systemctlPath string,
	unit string,
	digest string,
	executor Executor,
) error {
	output, err := executor.Run(ctx, Command{Path: systemctlPath, Args: []string{"cat", unit}})
	if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes {
		return errors.New("read pinned service unit definition")
	}
	if sha256Hex(output) != digest {
		return errors.New("service unit definition SHA-256 differs from the protected manifest")
	}
	return nil
}

func validatePinnedArtifactVersion(ctx context.Context, manifest Manifest, pin ArtifactPin, executor Executor) error {
	command := Command{Path: pin.Path, Args: []string{"--version"}}
	want := pin.Kind + " " + pin.Version
	if pin.Kind == "comis-cli" {
		command = Command{Path: manifest.Comis.NodePath, Args: []string{pin.Path, "--version"}}
		want = pin.Version
	}
	output, err := executor.Run(ctx, command)
	if err != nil {
		return errors.New("read pinned artifact version")
	}
	if strings.TrimSpace(string(output)) != want {
		return errors.New("reported artifact version differs from the protected manifest")
	}
	return nil
}

func validatePinnedArtifact(pin ArtifactPin) error {
	if err := validateCanonicalRegularFile(pin.Path); err != nil {
		return err
	}
	if pin.Kind != "comis-cli" {
		if err := validateExecutable(pin.Path); err != nil {
			return err
		}
	}
	contents, err := os.ReadFile(pin.Path)
	if err != nil {
		return errors.New("read pinned artifact")
	}
	if sha256Hex(contents) != pin.SHA256 {
		return errors.New("artifact SHA-256 differs from the protected manifest")
	}
	return nil
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func validateExecutable(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("executable path must be one clean absolute path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return errors.New("executable path is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("executable path must identify one executable regular file")
	}
	return nil
}

func validateCanonicalRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("path must identify one regular file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("path must be canonical and symlink-free")
	}
	return nil
}

func validateCanonicalDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return errors.New("path must identify one directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("path must be canonical and symlink-free")
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

var inheritedCommandEnvironment = []string{
	"DBUS_SESSION_BUS_ADDRESS", "HOME", "LANG", "LC_ALL", "PATH", "SSL_CERT_DIR", "SSL_CERT_FILE",
	"TMPDIR", "TZ", "XDG_RUNTIME_DIR",
}

var commandEnvironmentOverrides = map[string]struct{}{
	"COMIS_CONFIG_PATHS": {}, "COMIS_DATA_DIR": {}, "COMIS_SRC": {},
	"DEVCREW_INSTALL_DIR": {}, "DEVCREW_LINK_DIR": {}, "DEVCREW_VERSION": {},
	"GO_WANT_LIVE_CAMPAIGN_HELPER": {}, "HOME": {}, "LIVE_CAMPAIGN_TEST_VALUE": {},
	"NPM_CONFIG_PREFIX": {}, "PATH": {}, "RIG_MODE": {},
}

func allowedCommandEnvironmentOverride(name string) bool {
	_, found := commandEnvironmentOverrides[name]
	return found
}

func protectedCommandEnvironment(
	overrides map[string]string,
	useGitHubToken bool,
	useComisGatewayToken bool,
) ([]string, error) {
	values := make(map[string]string)
	for _, name := range inheritedCommandEnvironment {
		if value, found := os.LookupEnv(name); found {
			values[name] = value
		}
	}
	for name, value := range overrides {
		values[name] = value
	}
	if useGitHubToken {
		token := os.Getenv("GH_TOKEN")
		if token == "" {
			return nil, errors.New("execute protected command: GitHub credential is unavailable")
		}
		values["GH_TOKEN"] = token
	}
	if useComisGatewayToken {
		token := os.Getenv("COMIS_GATEWAY_TOKEN")
		if token == "" {
			return nil, errors.New("execute protected command: Comis gateway credential is unavailable")
		}
		values["COMIS_GATEWAY_TOKEN"] = token
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result, nil
}

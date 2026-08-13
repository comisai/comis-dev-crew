package livecampaign

import (
	"bytes"
	"context"
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
		if !validEnvironmentName(name) || strings.ContainsRune(value, 0) {
			return nil, errors.New("execute protected command: environment override is invalid")
		}
	}
	process := exec.CommandContext(ctx, command.Path, command.Args...)
	process.Env = mergedEnvironment(command.Env)
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
	if err := manifest.validate(); err != nil {
		return fmt.Errorf("validate protected runtime: %w", err)
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
	for name, path := range map[string]string{
		"Comis CLI script":        manifest.Comis.CLIScriptPath,
		"secret residency script": manifest.Comis.SecretResidencyScript,
	} {
		if err := validateCanonicalRegularFile(path); err != nil {
			return fmt.Errorf("validate protected runtime: %s: %w", name, err)
		}
		if !pathWithin(manifest.Comis.CodeRoot, path) {
			return fmt.Errorf("validate protected runtime: %s is outside the pinned Comis code root", name)
		}
	}
	for name, path := range map[string]string{
		"Comis code root":      manifest.Comis.CodeRoot,
		"Comis data root":      manifest.Comis.DataDir,
		"Git primary checkout": manifest.GitHub.PrimaryCheckout,
	} {
		if err := validateCanonicalDirectory(path); err != nil {
			return fmt.Errorf("validate protected runtime: %s: %w", name, err)
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

func validEnvironmentName(name string) bool {
	if name == "" || !(name[0] == '_' || name[0] >= 'A' && name[0] <= 'Z' || name[0] >= 'a' && name[0] <= 'z') {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if character != '_' && !(character >= 'A' && character <= 'Z') &&
			!(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, found := strings.Cut(item, "=")
		if found {
			values[name] = value
		}
	}
	for name, value := range overrides {
		values[name] = value
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
	return result
}

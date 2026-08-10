package workers_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func TestProfileCatalog_BuildsOneExactNoShellLaunchDescriptor(t *testing.T) {
	executable := codexFixtureExecutable(t)
	profile := availableCodexProfile(executable, "codex-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatalf("NewProfileCatalog() error = %v", err)
	}
	workingDirectory := canonicalWorkerTempDir(t)
	descriptor, err := catalog.BuildLaunchDescriptor(workers.LaunchRequest{
		ProfileID: profile.ID, Shape: domain.ShapeShip, WorkingDirectory: workingDirectory,
	})
	if err != nil {
		t.Fatalf("BuildLaunchDescriptor() error = %v", err)
	}
	if descriptor.ProfileID != profile.ID || descriptor.Harness != workers.HarnessCodex ||
		descriptor.Executable != executable || descriptor.WorkingDirectory != workingDirectory ||
		descriptor.Model != "gpt-5.5-codex" || descriptor.Effort != "high" ||
		descriptor.TerminalAllowEntry != "codex-confined" || descriptor.Network != workers.NetworkRestricted ||
		descriptor.ConcurrencyLimit != 2 || descriptor.Unattended {
		t.Fatalf("launch descriptor = %#v", descriptor)
	}
	if !reflect.DeepEqual(descriptor.Arguments, []string{"exec", "--json"}) ||
		!reflect.DeepEqual(descriptor.EnvironmentKeys, []string{"DEV_CREW_ATTACHMENT", "PATH"}) {
		t.Fatalf("descriptor vectors = args:%q env:%q", descriptor.Arguments, descriptor.EnvironmentKeys)
	}
	descriptor.Arguments[0] = "altered"
	descriptor.EnvironmentKeys[0] = "ALTERED"
	replayed, err := catalog.BuildLaunchDescriptor(workers.LaunchRequest{
		ProfileID: profile.ID, Shape: domain.ShapeShip, WorkingDirectory: workingDirectory,
	})
	if err != nil || replayed.Arguments[0] != "exec" || replayed.EnvironmentKeys[0] != "DEV_CREW_ATTACHMENT" {
		t.Fatalf("catalog vectors were mutable: %#v, %v", replayed, err)
	}
}

func TestProfileCatalog_ReturnsExactUnavailableUnknownAndShapeErrorsWithoutFallback(t *testing.T) {
	executable := codexFixtureExecutable(t)
	unavailable := availableCodexProfile(executable, "codex-auth-missing")
	unavailable.Availability = workers.AvailabilityUnavailable
	unavailable.AvailabilityReason = workers.AvailabilityReasonAuthentication
	available := availableCodexProfile(executable, "codex-fallback-must-not-run")
	unknown := availableCodexProfile(executable, "codex-not-probed")
	unknown.Availability = workers.AvailabilityUnknown
	unknown.AvailabilityReason = workers.AvailabilityReasonNotProbed
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{unavailable, available, unknown})
	if err != nil {
		t.Fatal(err)
	}
	request := workers.LaunchRequest{
		ProfileID: unavailable.ID, Shape: domain.ShapeShip, WorkingDirectory: canonicalWorkerTempDir(t),
	}
	_, err = catalog.BuildLaunchDescriptor(request)
	var availability *workers.ProfileAvailabilityError
	if !errors.As(err, &availability) || availability.ProfileID != unavailable.ID ||
		availability.Availability != workers.AvailabilityUnavailable ||
		availability.Reason != workers.AvailabilityReasonAuthentication {
		t.Fatalf("unavailable profile error = %#v / %v", availability, err)
	}
	if got := err.Error(); got != "worker profile codex-auth-missing is unavailable: authentication_unavailable" {
		t.Fatalf("unavailable profile diagnostic = %q", got)
	}
	request.ProfileID = unknown.ID
	if _, err := catalog.BuildLaunchDescriptor(request); !errors.As(err, &availability) ||
		availability.Availability != workers.AvailabilityUnknown || availability.Reason != workers.AvailabilityReasonNotProbed {
		t.Fatalf("unknown availability error = %#v / %v", availability, err)
	}
	request.ProfileID = "missing-profile"
	if _, err := catalog.BuildLaunchDescriptor(request); !errors.Is(err, workers.ErrProfileUnknown) {
		t.Fatalf("missing profile error = %v", err)
	}
	request.ProfileID = available.ID
	request.Shape = domain.ShapeScout
	if _, err := catalog.BuildLaunchDescriptor(request); !errors.Is(err, workers.ErrProfileShapeUnsupported) {
		t.Fatalf("shape mismatch error = %v", err)
	}
}

func TestProfileCatalog_RejectsUnavailableCatalogAndUnsafeWorkingDirectories(t *testing.T) {
	var unavailable *workers.ProfileCatalog
	if _, err := unavailable.ResolveProfile("codex-reviewed", domain.ShapeShip); err == nil {
		t.Fatal("ResolveProfile(unavailable catalog) error = nil")
	}
	profile := availableCodexProfile(codexFixtureExecutable(t), "codex-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	root := canonicalWorkerTempDir(t)
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(root, linked); err != nil {
		t.Fatal(err)
	}
	for _, workingDirectory := range []string{
		"relative", regular, linked, filepath.Join(root, "missing"), root + "\n",
	} {
		if _, err := catalog.BuildLaunchDescriptor(workers.LaunchRequest{
			ProfileID: profile.ID, Shape: domain.ShapeShip, WorkingDirectory: workingDirectory,
		}); err == nil {
			t.Fatalf("BuildLaunchDescriptor(%q) error = nil", workingDirectory)
		}
	}
}

func TestProfileCatalog_RejectsUnsafeOrAmbiguousStaticProfiles(t *testing.T) {
	executable := codexFixtureExecutable(t)
	valid := availableCodexProfile(executable, "codex-reviewed")
	symlink := filepath.Join(canonicalWorkerTempDir(t), "codex")
	if err := os.Symlink(executable, symlink); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		profiles []workers.StaticProfile
	}{
		{name: "none"},
		{name: "duplicate", profiles: []workers.StaticProfile{valid, valid}},
		{name: "unknown harness", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Harness = "other" })},
		{name: "unknown shape", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.AllowedShapes = []domain.TaskShape{"review"} })},
		{name: "relative executable", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Executable = "codex" })},
		{name: "symlink executable", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Executable = symlink })},
		{name: "shell executable", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Executable = "/bin/sh" })},
		{name: "invalid arguments", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Arguments = []string{"exec\nrm"} })},
		{name: "too many arguments", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) {
			profile.Arguments = make([]string, 33)
			for index := range profile.Arguments {
				profile.Arguments[index] = "arg"
			}
		})},
		{name: "duplicate environment", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.EnvironmentKeys = []string{"PATH", "PATH"} })},
		{name: "invalid environment", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.EnvironmentKeys = []string{"PATH=value"} })},
		{name: "too many environment keys", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) {
			profile.EnvironmentKeys = make([]string, 33)
			for index := range profile.EnvironmentKeys {
				profile.EnvironmentKeys[index] = "KEY_" + string(rune('A'+index))
			}
		})},
		{name: "missing model", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Model = "" })},
		{name: "invalid availability", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Availability = "ready" })},
		{name: "available with reason", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.AvailabilityReason = workers.AvailabilityReasonNotProbed })},
		{name: "unavailable without reason", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.Availability = workers.AvailabilityUnavailable })},
		{name: "invalid concurrency", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) { profile.ConcurrencyLimit = 0 })},
		{name: "duplicate shape", profiles: mutateProfile(valid, func(profile *workers.StaticProfile) {
			profile.AllowedShapes = []domain.TaskShape{domain.ShapeShip, domain.ShapeShip}
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := workers.NewProfileCatalog(test.profiles); err == nil {
				t.Fatal("NewProfileCatalog() error = nil")
			}
		})
	}
}

func availableCodexProfile(executable, id string) workers.StaticProfile {
	return workers.StaticProfile{
		ID: id, Harness: workers.HarnessCodex, AllowedShapes: []domain.TaskShape{domain.ShapeShip},
		Model: "gpt-5.5-codex", Effort: "high", TerminalAllowEntry: "codex-confined",
		Network: workers.NetworkRestricted, ConcurrencyLimit: 2,
		Executable: executable, Arguments: []string{"exec", "--json"},
		EnvironmentKeys: []string{"DEV_CREW_ATTACHMENT", "PATH"},
		Availability:    workers.AvailabilityAvailable,
	}
}

func mutateProfile(profile workers.StaticProfile, mutate func(*workers.StaticProfile)) []workers.StaticProfile {
	mutate(&profile)
	return []workers.StaticProfile{profile}
}

func codexFixtureExecutable(t *testing.T) string {
	t.Helper()
	source, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(canonicalWorkerTempDir(t), "codex")
	if err := os.WriteFile(target, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}

func canonicalWorkerTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

package validation

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestProfileCatalog_ResolvesOnlyReviewedArgumentTemplates(t *testing.T) {
	catalog, err := NewCatalog(CatalogConfig{
		Programs: []Program{{ID: "go-test", Executable: "/usr/bin/go"}},
		Profiles: []Profile{{
			ID:        "fixture-default",
			PathRules: []PathRule{{Kind: PathRuleExact, Path: "report.md"}},
			LocalChecks: []LocalCheck{{
				ID: "unit", ProgramID: "go-test", Timeout: 2 * time.Minute, Required: true,
				Arguments: []ArgumentTemplate{
					{Kind: ArgumentLiteral, Value: "test"},
					{Kind: ArgumentLiteral, Value: "./..."},
					{Kind: ArgumentTaskField, Value: string(FieldHeadRevision)},
				},
			}},
			ForgeChecks:   []ForgeCheck{{Name: "ci/unit", Required: true}},
			ArtifactRules: []ArtifactRule{{Kind: ArtifactRegularFile, RelativePath: "report.md", MediaType: "text/markdown", MaxBytes: 1024}},
			EvidenceTTL:   15 * time.Minute,
		}},
	})
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	command, err := catalog.ResolveLocalCheck("fixture-default", "unit", TaskFields{
		TaskHandle: "task-alpha", WorktreePath: "/approved/worktrees/task-alpha",
		BaseRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", HeadRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil {
		t.Fatalf("ResolveLocalCheck() error = %v", err)
	}
	if command.Executable != "/usr/bin/go" || command.WorkingDirectory != "/approved/worktrees/task-alpha" ||
		!reflect.DeepEqual(command.Arguments, []string{"test", "./...", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}) ||
		command.Timeout != 2*time.Minute || !command.Required {
		t.Fatalf("resolved command = %#v", command)
	}
	profile, err := catalog.ResolveProfile("fixture-default")
	if err != nil || len(profile.ForgeChecks) != 1 || len(profile.PathRules) != 1 || len(profile.ArtifactRules) != 1 ||
		!profile.AllowsPath("report.md") || profile.AllowsPath("generated/report.md") ||
		profile.ArtifactRules[0].RelativePath != "report.md" || profile.ArtifactRules[0].MediaType != "text/markdown" ||
		profile.EvidenceTTL != 15*time.Minute {
		t.Fatalf("ResolveProfile() = %#v, %v", profile, err)
	}
	profile.LocalChecks[0].Arguments[0].Value = "mutated"
	profile.PathRules[0].Path = "mutated.md"
	again, err := catalog.ResolveLocalCheck("fixture-default", "unit", TaskFields{
		TaskHandle: "task-alpha", WorktreePath: "/approved/worktrees/task-alpha",
		BaseRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", HeadRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if err != nil || again.Arguments[0] != "test" {
		t.Fatalf("catalog was mutable through resolved profile: %#v, %v", again, err)
	}
}

func TestProfileCatalog_ResolvesOnlyShapeCompleteProfiles(t *testing.T) {
	complete := Profile{
		ID:        "fixture-default",
		PathRules: []PathRule{{Kind: PathRulePrefix, Path: "src/"}},
		LocalChecks: []LocalCheck{{
			ID: "unit", ProgramID: "go-test", Timeout: time.Minute, Required: true,
			Arguments: []ArgumentTemplate{{Kind: ArgumentLiteral, Value: "test"}},
		}},
		ForgeChecks:   []ForgeCheck{{Name: "ci/unit", Required: true}},
		ArtifactRules: []ArtifactRule{{Kind: ArtifactRegularFile, RelativePath: "report.md", MediaType: "text/markdown", MaxBytes: 1024}},
		EvidenceTTL:   time.Minute,
	}
	tests := []struct {
		name    string
		shape   domain.TaskShape
		mutate  func(*Profile)
		wantErr bool
	}{
		{name: "complete ship", shape: domain.ShapeShip},
		{name: "complete scout", shape: domain.ShapeScout},
		{name: "ship without required local", shape: domain.ShapeShip, wantErr: true, mutate: func(profile *Profile) {
			profile.LocalChecks[0].Required = false
		}},
		{name: "ship without required forge", shape: domain.ShapeShip, wantErr: true, mutate: func(profile *Profile) {
			profile.ForgeChecks[0].Required = false
		}},
		{name: "scout without artifact", shape: domain.ShapeScout, wantErr: true, mutate: func(profile *Profile) {
			profile.ArtifactRules = nil
		}},
		{name: "unknown shape", shape: domain.TaskShape("initiative"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := cloneProfile(complete)
			if test.mutate != nil {
				test.mutate(&profile)
			}
			catalog, err := NewCatalog(CatalogConfig{
				Programs: []Program{{ID: "go-test", Executable: "/usr/bin/go"}}, Profiles: []Profile{profile},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = catalog.ResolveProfileForShape(profile.ID, test.shape)
			if (err != nil) != test.wantErr {
				t.Fatalf("ResolveProfileForShape(%s) error = %v, want error %t", test.shape, err, test.wantErr)
			}
		})
	}
}

func TestProfileCatalog_RejectsUnreviewedProgramsAndAmbiguousProfiles(t *testing.T) {
	validProgram := Program{ID: "go-test", Executable: "/usr/bin/go"}
	validProfile := Profile{
		ID:        "fixture-default",
		PathRules: []PathRule{{Kind: PathRuleExact, Path: "report.md"}},
		LocalChecks: []LocalCheck{{
			ID: "unit", ProgramID: "go-test", Timeout: time.Minute, Required: true,
			Arguments: []ArgumentTemplate{{Kind: ArgumentLiteral, Value: "test"}},
		}},
		EvidenceTTL: time.Minute,
	}
	tests := []struct {
		name   string
		mutate func(*CatalogConfig)
	}{
		{name: "relative executable", mutate: func(config *CatalogConfig) { config.Programs[0].Executable = "go" }},
		{name: "duplicate program", mutate: func(config *CatalogConfig) { config.Programs = append(config.Programs, validProgram) }},
		{name: "duplicate profile", mutate: func(config *CatalogConfig) { config.Profiles = append(config.Profiles, validProfile) }},
		{name: "unknown program", mutate: func(config *CatalogConfig) { config.Profiles[0].LocalChecks[0].ProgramID = "missing" }},
		{name: "shell fragment", mutate: func(config *CatalogConfig) {
			config.Profiles[0].LocalChecks[0].Arguments[0].Value = "test; touch escaped"
		}},
		{name: "unknown field", mutate: func(config *CatalogConfig) {
			config.Profiles[0].LocalChecks[0].Arguments[0] = ArgumentTemplate{Kind: ArgumentTaskField, Value: "worker_argument"}
		}},
		{name: "zero timeout", mutate: func(config *CatalogConfig) { config.Profiles[0].LocalChecks[0].Timeout = 0 }},
		{name: "zero evidence ttl", mutate: func(config *CatalogConfig) { config.Profiles[0].EvidenceTTL = 0 }},
		{name: "excess local checks", mutate: func(config *CatalogConfig) {
			checks := make([]LocalCheck, maximumLocalChecks+1)
			for index := range checks {
				checks[index] = validProfile.LocalChecks[0]
				checks[index].ID = fmt.Sprintf("check-%02d", index)
			}
			config.Profiles[0].LocalChecks = checks
		}},
		{name: "missing path policy", mutate: func(config *CatalogConfig) { config.Profiles[0].PathRules = nil }},
		{name: "unknown path rule kind", mutate: func(config *CatalogConfig) {
			config.Profiles[0].PathRules = []PathRule{{Kind: PathRuleKind("glob"), Path: "*.go"}}
		}},
		{name: "excess path rules", mutate: func(config *CatalogConfig) {
			rules := make([]PathRule, maximumPathRules+1)
			for index := range rules {
				rules[index] = PathRule{Kind: PathRuleExact, Path: fmt.Sprintf("file-%02d.go", index)}
			}
			config.Profiles[0].PathRules = rules
		}},
		{name: "escaping path prefix", mutate: func(config *CatalogConfig) {
			config.Profiles[0].PathRules = []PathRule{{Kind: PathRulePrefix, Path: "../generated/"}}
		}},
		{name: "duplicated path rule", mutate: func(config *CatalogConfig) {
			config.Profiles[0].PathRules = append(config.Profiles[0].PathRules, config.Profiles[0].PathRules[0])
		}},
		{name: "reserved local check", mutate: func(config *CatalogConfig) {
			config.Profiles[0].LocalChecks[0].ID = CandidatePathPolicyCheckID
		}},
		{name: "unbounded artifact", mutate: func(config *CatalogConfig) {
			config.Profiles[0].ArtifactRules = []ArtifactRule{{Kind: ArtifactRegularFile}}
		}},
		{name: "escaping artifact", mutate: func(config *CatalogConfig) {
			config.Profiles[0].ArtifactRules = []ArtifactRule{{
				Kind: ArtifactRegularFile, RelativePath: "../report.md", MediaType: "text/markdown", MaxBytes: 1024,
			}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := CatalogConfig{Programs: []Program{validProgram}, Profiles: []Profile{validProfile}}
			configuration.Profiles[0].LocalChecks = append([]LocalCheck(nil), validProfile.LocalChecks...)
			configuration.Profiles[0].LocalChecks[0].Arguments = append([]ArgumentTemplate(nil), validProfile.LocalChecks[0].Arguments...)
			configuration.Profiles[0].PathRules = append([]PathRule(nil), validProfile.PathRules...)
			test.mutate(&configuration)
			if _, err := NewCatalog(configuration); err == nil {
				t.Fatal("NewCatalog() error = nil")
			}
		})
	}
}

func TestProfileCatalog_NamesMissingCandidatePathRules(t *testing.T) {
	_, err := NewCatalog(CatalogConfig{
		Programs: []Program{{ID: "go-test", Executable: "/usr/bin/go"}},
		Profiles: []Profile{{
			ID: "fixture-default",
			LocalChecks: []LocalCheck{{
				ID: "unit", ProgramID: "go-test", Timeout: time.Minute, Required: true,
				Arguments: []ArgumentTemplate{{Kind: ArgumentLiteral, Value: "test"}},
			}},
			EvidenceTTL: time.Minute,
		}},
	})
	if err == nil || err.Error() != "create validation catalog: profile path rules are required" {
		t.Fatalf("NewCatalog() error = %v, want missing path-rules diagnostic", err)
	}
}

func TestProfileCatalog_RejectsUnknownProfilesChecksAndTaskFacts(t *testing.T) {
	if _, err := (*Catalog)(nil).ResolveProfile("fixture-default"); err == nil {
		t.Fatal("ResolveProfile(nil) error = nil")
	}
	catalog, err := NewCatalog(CatalogConfig{
		Programs: []Program{{ID: "go-test", Executable: "/usr/bin/go"}},
		Profiles: []Profile{{ID: "fixture-default", EvidenceTTL: time.Minute,
			PathRules: []PathRule{{Kind: PathRuleExact, Path: "report.md"}}, LocalChecks: []LocalCheck{{
				ID: "unit", ProgramID: "go-test", Timeout: time.Minute, Required: true,
				Arguments: []ArgumentTemplate{{Kind: ArgumentTaskField, Value: string(FieldTaskHandle)}},
			}}}},
	})
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	if _, err := catalog.ResolveProfile("missing-profile"); err == nil {
		t.Fatal("ResolveProfile(missing) error = nil")
	}
	validFields := TaskFields{
		TaskHandle: "task-alpha", WorktreePath: "/approved/worktrees/task-alpha",
		BaseRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", HeadRevision: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	if _, err := catalog.ResolveLocalCheck("fixture-default", "missing-check", validFields); err == nil {
		t.Fatal("ResolveLocalCheck(missing) error = nil")
	}
	invalidFields := validFields
	invalidFields.WorktreePath = "relative/worktree"
	if _, err := catalog.ResolveLocalCheck("fixture-default", "unit", invalidFields); err == nil {
		t.Fatal("ResolveLocalCheck(relative worktree) error = nil")
	}
}

func TestProfileCatalogAllowsOnlyReviewedExactAndPrefixPaths(t *testing.T) {
	profile := Profile{PathRules: []PathRule{
		{Kind: PathRuleExact, Path: "package.json"},
		{Kind: PathRulePrefix, Path: "backend/"},
	}}
	for _, test := range []struct {
		path string
		want bool
	}{
		{path: "package.json", want: true},
		{path: "backend/api/handler.go", want: true},
		{path: "backendish/handler.go", want: false},
		{path: "frontend/app.ts", want: false},
		{path: "backend/../frontend/app.ts", want: false},
		{path: "/backend/app.go", want: false},
	} {
		if got := profile.AllowsPath(test.path); got != test.want {
			t.Fatalf("AllowsPath(%q) = %t, want %t", test.path, got, test.want)
		}
	}
}

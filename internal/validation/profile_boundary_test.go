package validation

import (
	"testing"
	"time"
)

func boundaryLocalCheck(id string) LocalCheck {
	return LocalCheck{
		ID: id, ProgramID: "go-test", Timeout: time.Minute, Required: true,
		Arguments: []ArgumentTemplate{{Kind: ArgumentLiteral, Value: "test"}},
	}
}

func boundaryProfile(profile Profile) CatalogConfig {
	return CatalogConfig{
		Programs: []Program{{ID: "go-test", Executable: "/usr/bin/go"}},
		Profiles: []Profile{profile},
	}
}

func TestProfileCatalogRejectsAmbiguousCheckAndArtifactShapes(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
	}{
		{
			name: "duplicated local check",
			profile: Profile{
				ID:          "fixture-duplicate-local",
				LocalChecks: []LocalCheck{boundaryLocalCheck("unit"), boundaryLocalCheck("unit")},
				EvidenceTTL: time.Minute,
			},
		},
		{
			name: "untrimmed forge check",
			profile: Profile{
				ID:          "fixture-untrimmed-forge",
				LocalChecks: []LocalCheck{boundaryLocalCheck("unit")},
				ForgeChecks: []ForgeCheck{{Name: " ci/unit "}},
				EvidenceTTL: time.Minute,
			},
		},
		{
			name: "duplicated forge check",
			profile: Profile{
				ID:          "fixture-duplicate-forge",
				LocalChecks: []LocalCheck{boundaryLocalCheck("unit")},
				ForgeChecks: []ForgeCheck{{Name: "ci/unit"}, {Name: "ci/unit"}},
				EvidenceTTL: time.Minute,
			},
		},
		{
			name: "ambiguous artifact rules",
			profile: Profile{
				ID:          "fixture-ambiguous-artifacts",
				LocalChecks: []LocalCheck{boundaryLocalCheck("unit")},
				ArtifactRules: []ArtifactRule{
					{Kind: ArtifactRegularFile, RelativePath: "one.md", MediaType: "text/markdown", MaxBytes: 16},
					{Kind: ArtifactRegularFile, RelativePath: "two.md", MediaType: "text/markdown", MaxBytes: 16},
				},
				EvidenceTTL: time.Minute,
			},
		},
		{
			name: "escaping artifact path",
			profile: Profile{
				ID:          "fixture-escaping-artifact",
				LocalChecks: []LocalCheck{boundaryLocalCheck("unit")},
				ArtifactRules: []ArtifactRule{
					{Kind: ArtifactRegularFile, RelativePath: ".", MediaType: "text/markdown", MaxBytes: 16},
				},
				EvidenceTTL: time.Minute,
			},
		},
		{
			name: "unreviewed argument kind",
			profile: Profile{
				ID: "fixture-unreviewed-argument",
				LocalChecks: []LocalCheck{{
					ID: "unit", ProgramID: "go-test", Timeout: time.Minute,
					Arguments: []ArgumentTemplate{{Kind: ArgumentKind("unreviewed"), Value: "test"}},
				}},
				EvidenceTTL: time.Minute,
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCatalog(boundaryProfile(test.profile)); err == nil {
				t.Fatalf("NewCatalog(%s) accepted an invalid profile", test.name)
			}
		})
	}
}

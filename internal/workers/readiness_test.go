package workers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

// Dispatch asks the adapter whether it can run an exact profile for an exact
// shape. The answer names the condition: a caller told only "no" would have to
// guess between a profile that belongs to another family and one that belongs
// here but cannot run scouts, and those have different repairs.
func TestAdapters_ValidateProfileNamesTheExactRefusal(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			owned := interventionProfileID(name)
			if err := adapter.ValidateProfile(context.Background(), owned, domain.ShapeShip); err != nil {
				t.Fatalf("ValidateProfile(%q, ship) error = %v", owned, err)
			}
			if err := adapter.ValidateProfile(context.Background(), "someone-elses-profile", domain.ShapeShip); !errors.Is(err, workers.ErrProfileUnknown) {
				t.Errorf("ValidateProfile(foreign) error = %v, want ErrProfileUnknown", err)
			}
			if err := adapter.ValidateProfile(context.Background(), owned, domain.TaskShape("archaeology")); err == nil {
				t.Error("ValidateProfile(unknown shape) error = nil")
			}
		})
	}
}

// A profile that allows only one shape must refuse the other by name, so
// preparation reports the exact posture rather than a generic unavailability.
func TestAdapters_ValidateProfileRefusesAShapeTheProfileDoesNotAllow(t *testing.T) {
	profile := availableCodexProfile(codexFixtureExecutable(t), "codex-shiponly")
	profile.AllowedShapes = []domain.TaskShape{domain.ShapeShip}
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.ValidateProfile(context.Background(), profile.ID, domain.ShapeShip); err != nil {
		t.Fatalf("ValidateProfile(ship) error = %v", err)
	}
	if err := adapter.ValidateProfile(context.Background(), profile.ID, domain.ShapeScout); !errors.Is(err, workers.ErrProfileShapeUnsupported) {
		t.Fatalf("ValidateProfile(scout) error = %v, want ErrProfileShapeUnsupported", err)
	}
}

// Diagnose is the family's own bounded readiness. It reports the settle-signal
// posture separately from availability: a harness can be installed, pinned and
// reachable while still unable to prove a turn ended, and conflating the two
// would let an unattended profile run on a signal nobody verified.
func TestAdapters_DiagnoseReportsSettlePostureSeparatelyFromAvailability(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			diagnosis, err := adapter.Diagnose(context.Background())
			if err != nil {
				t.Fatalf("Diagnose() error = %v", err)
			}
			if diagnosis.Harness != adapter.ID() {
				t.Errorf("diagnosis harness = %q, want %q", diagnosis.Harness, adapter.ID())
			}
			if diagnosis.ProfileID != interventionProfileID(name) {
				t.Errorf("diagnosis profile = %q", diagnosis.ProfileID)
			}
			// These adapters were built without a verified settle signal, so
			// the posture must say so and the reason must name it.
			if diagnosis.SettleSignalVerified {
				t.Error("diagnosis claims a settle signal that was never verified")
			}
			if diagnosis.LifecycleReason != application.HarnessReasonLifecycleSignalUnknown {
				t.Errorf("diagnosis lifecycle reason = %q, want the unknown-signal reason", diagnosis.LifecycleReason)
			}
			// Content-free: a diagnosis travels to operator surfaces, so it
			// carries identities and closed codes, never installed paths.
			if diagnosis.ExpectedVersion == "" {
				t.Error("diagnosis omits the pinned version it was reviewed against")
			}
		})
	}
}

// Process attribution is evidence, not inference. Anything short of an exactly
// attributed observation is unknown, because a role assigned to the wrong
// process is how an unrelated program gets treated as task state.
func TestAdapters_ClassifyProcessRoleRefusesToGuessFromPartialAttribution(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			attributed := application.ProcessObservation{
				TaskHandle: "task-0001", Attributed: true,
				Source: application.ProcessSourceTerminalDescendant,
				Executable: "worker", ProfileID: interventionProfileID(name),
			}
			role := adapter.ClassifyProcessRole(attributed)
			if role.Role != application.ProcessRoleWorker {
				t.Errorf("attributed terminal descendant role = %q, want worker", role.Role)
			}

			validation := attributed
			validation.Source = application.ProcessSourceServiceLaunched
			validation.Executable = "validation"
			if got := adapter.ClassifyProcessRole(validation); got.Role != application.ProcessRoleValidation {
				t.Errorf("service-launched validation role = %q, want validation", got.Role)
			}

			for label, observation := range map[string]application.ProcessObservation{
				"unattributed": {TaskHandle: "task-0001", Source: application.ProcessSourceTerminalDescendant, Executable: "worker"},
				"no task":      {Attributed: true, Source: application.ProcessSourceTerminalDescendant, Executable: "worker"},
				"no source":    {TaskHandle: "task-0001", Attributed: true, Executable: "worker"},
				"foreign profile": {
					TaskHandle: "task-0001", Attributed: true, ProfileID: "someone-elses-profile",
					Source: application.ProcessSourceTerminalDescendant, Executable: "worker",
				},
			} {
				if got := adapter.ClassifyProcessRole(observation); got.Role != application.ProcessRoleUnknown {
					t.Errorf("%s role = %q, want unknown", label, got.Role)
				}
			}
		})
	}
}

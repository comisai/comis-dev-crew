package workers

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ValidateProfile confirms this family can run the exact reviewed profile for
// the exact shape, and names the condition when it cannot. Resolution is
// delegated to the catalog rather than re-checked here: the catalog already
// owns which profiles exist and which shapes each allows, and a second copy of
// those rules would be free to disagree with the one dispatch actually uses.
func (adapter *CodexAdapter) ValidateProfile(
	ctx context.Context,
	profileID string,
	shape domain.TaskShape,
) error {
	if err := adapter.readyFor(ctx, profileID); err != nil {
		return err
	}
	_, err := adapter.profiles.ResolveProfile(profileID, shape)
	return err
}

// Diagnose reports this family's bounded readiness.
func (adapter *CodexAdapter) Diagnose(ctx context.Context) (application.HarnessDiagnosis, error) {
	probe, err := adapter.ProbeVersion(ctx)
	if err != nil {
		return application.HarnessDiagnosis{}, err
	}
	return harnessDiagnosis(
		adapter.ID(), adapter.profileID, adapter.expectedVersion, adapter.settleSignalVerified, probe,
	), nil
}

// ClassifyProcessRole names the part an exactly attributed process plays.
func (adapter *CodexAdapter) ClassifyProcessRole(observation application.TaskProcessObservation) application.ProcessRoleResult {
	if result, ok := application.ClassifyAttributedProcessRole(observation, adapter.profileIdentity()); !ok {
		return result
	}
	return application.ClassifyProcessRoleBySource(observation)
}

// ValidateProfile confirms this family can run the exact reviewed profile for
// the exact shape, and names the condition when it cannot.
func (adapter *ClaudeAdapter) ValidateProfile(
	ctx context.Context,
	profileID string,
	shape domain.TaskShape,
) error {
	if err := adapter.readyFor(ctx, profileID); err != nil {
		return err
	}
	_, err := adapter.profiles.ResolveProfile(profileID, shape)
	return err
}

// Diagnose reports this family's bounded readiness.
func (adapter *ClaudeAdapter) Diagnose(ctx context.Context) (application.HarnessDiagnosis, error) {
	probe, err := adapter.ProbeVersion(ctx)
	if err != nil {
		return application.HarnessDiagnosis{}, err
	}
	return harnessDiagnosis(
		adapter.ID(), adapter.profileID, adapter.expectedVersion, adapter.settleSignalVerified, probe,
	), nil
}

// ClassifyProcessRole names the part an exactly attributed process plays.
func (adapter *ClaudeAdapter) ClassifyProcessRole(observation application.TaskProcessObservation) application.ProcessRoleResult {
	if result, ok := application.ClassifyAttributedProcessRole(observation, adapter.profileIdentity()); !ok {
		return result
	}
	return application.ClassifyProcessRoleBySource(observation)
}

// harnessDiagnosis assembles the shared bounded posture. The settle signal is
// carried separately from availability because an installed, pinned, reachable
// harness can still be unable to prove a worker turn ended, and only the
// unattended decision depends on that second fact.
func harnessDiagnosis(
	harness, profileID, expectedVersion string,
	settleSignalVerified bool,
	probe application.HarnessVersionProbe,
) application.HarnessDiagnosis {
	diagnosis := application.HarnessDiagnosis{
		Harness: harness, ProfileID: profileID, ExpectedVersion: expectedVersion,
		Version: probe.Version, Availability: probe.Availability, Reason: probe.Reason,
		SettleSignalVerified: settleSignalVerified,
	}
	if !settleSignalVerified {
		diagnosis.LifecycleReason = application.HarnessReasonLifecycleSignalUnknown
	}
	return diagnosis
}

package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) authorizeRebaseFinalization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	if request.PreparedRestorationRecovery {
		return registry.authorizePreparedRestorationFinalization(ctx, request, resultingHead)
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return err
	}
	snapshot, err := registry.integrationReceiptFamilySnapshot(ctx, request)
	if err != nil {
		return err
	}
	require := func(identity application.IntegrationAdapterRequest, outcome string, kind integrationReceiptKind, value string) error {
		return requireSnapshotReceipt(snapshot, integrationReceiptRef(outcome, identity), kind, value)
	}
	expectedTarget := "refs/heads/" + expectedIntegrationTargetBranch(request)
	original := originalIntegrationRequest(request)
	if require(original, "target", integrationReceiptSymbolic, expectedTarget) != nil {
		return errors.New("apply integration candidate: finalization target receipt differs")
	}
	if request.RecoveryOperationID == "" {
		if require(original, "conflicted", integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: finalization conflict receipt is contradictory")
		}
	} else {
		if require(original, "conflicted", integrationReceiptDirect, request.Target.ExpectedHead) != nil {
			return errors.New("apply integration candidate: finalization conflict receipt differs")
		}
		for _, outcome := range []string{"applied", "rebased"} {
			if require(original, outcome, integrationReceiptAbsent, "") != nil {
				return errors.New("apply integration candidate: original completion receipt is contradictory")
			}
		}
		for _, outcome := range []string{"target", "conflicted"} {
			if require(request, outcome, integrationReceiptAbsent, "") != nil {
				return errors.New("apply integration candidate: recovery authority receipt is contradictory")
			}
		}
	}
	if require(request, "applied", integrationReceiptAbsent, "") != nil {
		return errors.New("apply integration candidate: applied receipt is premature")
	}
	rebased := snapshot[integrationReceiptRef("rebased", request)]
	if rebased.kind != integrationReceiptAbsent &&
		(rebased.kind != integrationReceiptDirect || rebased.value != resultingHead) {
		return errors.New("apply integration candidate: rebased receipt differs")
	}
	return nil
}

func (registry *Registry) authorizePreparedRestorationFinalization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	if request.RecoveryOperationID == "" || !gitRevisionPattern.MatchString(resultingHead) {
		return errors.New("apply integration candidate: prepared recovery authority is invalid")
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return err
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return err
	}
	original := originalIntegrationRequest(request)
	snapshot, err := registry.integrationReceiptFamilySnapshot(ctx, request)
	if err != nil {
		return err
	}
	require := func(identity application.IntegrationAdapterRequest, outcome string, kind integrationReceiptKind, value string) error {
		return requireSnapshotReceipt(snapshot, integrationReceiptRef(outcome, identity), kind, value)
	}
	if require(original, "target", integrationReceiptSymbolic,
		"refs/heads/"+expectedIntegrationTargetBranch(request)) != nil {
		return errors.New("apply integration candidate: prepared recovery target receipt differs")
	}
	for _, identity := range []struct {
		outcome string
		request application.IntegrationAdapterRequest
	}{
		{"conflicted", original}, {"applied", original}, {"rebased", original},
		{"target", request}, {"conflicted", request}, {"applied", request},
	} {
		if require(identity.request, identity.outcome, integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: prepared recovery receipt family is contradictory")
		}
	}
	rebased := snapshot[integrationReceiptRef("rebased", request)]
	if rebased.kind != integrationReceiptAbsent &&
		(rebased.kind != integrationReceiptDirect || rebased.value != resultingHead) {
		return errors.New("apply integration candidate: prepared recovery rebased receipt differs")
	}
	proof := snapshot[integrationRebaseProofRef(original)]
	if proof.kind == integrationReceiptSymbolic ||
		proof.kind == integrationReceiptDirect && proof.value != original.Candidate.HeadRevision && proof.value != resultingHead ||
		proof.kind == integrationReceiptAbsent && rebased.kind != integrationReceiptDirect {
		return errors.New("apply integration candidate: prepared recovery proof receipt differs")
	}
	return nil
}

func (registry *Registry) requireSymbolicIntegrationReceipt(
	ctx context.Context,
	worktreePath string,
	reference string,
	want string,
) error {
	receipt, err := registry.inspectIntegrationReceipt(ctx, worktreePath, reference)
	if err != nil || receipt.kind != integrationReceiptSymbolic || receipt.value != want {
		return errors.New("symbolic integration receipt differs")
	}
	return nil
}

func (registry *Registry) requireDirectIntegrationReceipt(
	ctx context.Context,
	worktreePath string,
	reference string,
	want string,
) error {
	receipt, err := registry.inspectIntegrationReceipt(ctx, worktreePath, reference)
	if err != nil || receipt.kind != integrationReceiptDirect || receipt.value != want {
		return errors.New("direct integration receipt differs")
	}
	return nil
}

package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) resumePendingIntegrationMaterialization(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, bool, error) {
	if request.RecoveryOperationID == "" {
		return application.IntegrationAdapterResult{}, false, nil
	}
	original := originalIntegrationRequest(request)
	targetRef := "refs/heads/" + expectedIntegrationTargetBranch(request)
	resultingHead, found, err := registry.pendingIntegrationResult(ctx, repository, original)
	if err != nil || !found {
		return application.IntegrationAdapterResult{}, found, err
	}
	_, path, err := integrationMaterializationPath(repository, original)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	transition, found, err := readIntegrationMaterialization(path)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if !found {
		if !request.PendingMaterializationRecovery {
			return application.IntegrationAdapterResult{}, false, nil
		}
		if original.Strategy == application.IntegrationRebase {
			if pristine, pristineErr := registry.provePristinePublishedRebaseProof(ctx, repository, original); pristineErr != nil {
				return application.IntegrationAdapterResult{}, true, pristineErr
			} else if !pristine {
				return application.IntegrationAdapterResult{}, true,
					errors.New("apply integration candidate: pending rebase state is ambiguous")
			}
		} else if pristineErr := registry.provePristineIntegrationState(ctx, original, targetRef); pristineErr != nil {
			return application.IntegrationAdapterResult{}, true, pristineErr
		}
		return application.IntegrationAdapterResult{}, true, errors.Join(
			errors.New("apply integration candidate: original mutation did not start"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	if !integrationMaterializationMatches(transition, original, targetRef, resultingHead) {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: pending materialization identity differs")
	}
	if err := registry.validatePendingMaterializationReceiptSet(ctx, request, original, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	request.PendingMaterializationRecovery = true
	if err := registry.advanceIntegrationMaterialization(ctx, request, transition); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if request.Strategy == application.IntegrationRebase {
		if err := registry.authorizePendingMaterializationRecovery(ctx, request, resultingHead); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		if err := registry.retireIntegrationRebaseProof(ctx, request, resultingHead); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: pending materialization completed after authorization expired")
	}
	if err := registry.createIntegrationReceipt(
		ctx, repository, integrationReceiptRef("applied", request), resultingHead,
	); err != nil {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: recovered applied receipt could not be recorded")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: resultingHead,
	}, true, nil
}

func (registry *Registry) pendingIntegrationResult(
	ctx context.Context,
	repository Repository,
	original application.IntegrationAdapterRequest,
) (string, bool, error) {
	if original.Strategy == application.IntegrationRebase {
		proof, found, err := registry.serverRebaseProof(repository, original)
		if err != nil || !found || proof.resultingHead == "" {
			return "", false, err
		}
		if proof.operationID != original.OperationID {
			return "", true, errors.New("apply integration candidate: pending rebase proof differs")
		}
		if err := registry.requireServerRebaseProof(ctx, repository, original, proof.resultingHead); err != nil {
			return "", true, err
		}
		return proof.resultingHead, true, nil
	}
	_, path, err := serverIntegrationPlanPath(repository, original)
	if err != nil {
		return "", true, err
	}
	plan, found, err := readServerIntegrationPlan(path)
	if err != nil || !found {
		return "", false, err
	}
	if !serverIntegrationPlanMatches(plan, original) {
		return "", true, errors.New("apply integration candidate: pending isolated plan differs")
	}
	return plan.ResultingHead, true, nil
}

func (registry *Registry) validatePendingMaterializationReceiptSet(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	original application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	for _, identity := range []struct {
		outcome string
		request application.IntegrationAdapterRequest
	}{
		{"conflicted", original}, {"applied", original}, {"rebased", original},
		{"target", request}, {"conflicted", request}, {"applied", request}, {"rebased", request},
	} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef(identity.outcome, identity.request),
		); err != nil {
			return errors.New("apply integration candidate: pending materialization receipts differ")
		}
	}
	if request.Strategy != application.IntegrationRebase {
		return registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef("target", original),
		)
	}
	if err := registry.requireSymbolicIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationReceiptRef("target", original),
		"refs/heads/"+expectedIntegrationTargetBranch(request),
	); err != nil {
		return errors.New("apply integration candidate: pending rebase target receipt differs")
	}
	proof, err := registry.inspectIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationRebaseProofRef(original),
	)
	if err != nil || proof.kind != integrationReceiptDirect || proof.value != resultingHead {
		return errors.New("apply integration candidate: pending rebase result receipt differs")
	}
	return nil
}

func (registry *Registry) authorizePendingMaterializationRecovery(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	if !request.PendingMaterializationRecovery || request.RecoveryOperationID == "" {
		return errors.New("apply integration candidate: pending materialization authority is unavailable")
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return err
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return err
	}
	return registry.validatePendingMaterializationReceiptSet(
		ctx, request, originalIntegrationRequest(request), resultingHead,
	)
}

func (registry *Registry) provePristineIntegrationState(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
) error {
	if _, err := registry.expectedMaterializationIdentity(ctx, request, targetRef); err != nil {
		return errors.New("apply integration candidate: pristine target is unverified")
	}
	for _, outcome := range []string{"target", "conflicted", "applied", "rebased"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, request),
		); err != nil {
			return errors.New("apply integration candidate: pristine receipt set differs")
		}
	}
	return nil
}

func (registry *Registry) provePristinePublishedRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (bool, error) {
	targetRef := "refs/heads/" + expectedIntegrationTargetBranch(request)
	_, path, err := integrationMaterializationPath(repository, request)
	if err != nil {
		return false, err
	}
	if _, found, err := readIntegrationMaterialization(path); err != nil || found {
		return false, err
	}
	proof, err := registry.inspectIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationRebaseProofRef(request),
	)
	if err != nil || proof.kind != integrationReceiptAbsent {
		if err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err := registry.expectedMaterializationIdentity(ctx, request, targetRef); err != nil {
		return false, nil
	}
	target, err := registry.inspectIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationReceiptRef("target", request),
	)
	if err != nil || target.kind != integrationReceiptAbsent &&
		(target.kind != integrationReceiptSymbolic || target.value != targetRef) {
		return false, errors.New("apply integration candidate: pristine rebase target receipt differs")
	}
	for _, outcome := range []string{"conflicted", "applied", "rebased"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, request),
		); err != nil {
			return false, errors.New("apply integration candidate: pristine rebase receipt set differs")
		}
	}
	return true, nil
}

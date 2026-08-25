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
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return err
	}
	expectedTarget := "refs/heads/" + expectedIntegrationTargetBranch(request)
	original := originalIntegrationRequest(request)
	if err := registry.requireSymbolicIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationReceiptRef("target", original), expectedTarget,
	); err != nil {
		return errors.New("apply integration candidate: finalization target receipt differs")
	}
	if request.RecoveryOperationID == "" {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef("conflicted", original),
		); err != nil {
			return errors.New("apply integration candidate: finalization conflict receipt is contradictory")
		}
	} else {
		if err := registry.requireDirectIntegrationReceipt(
			ctx, request.Target.WorktreePath, integrationReceiptRef("conflicted", original), request.Target.ExpectedHead,
		); err != nil {
			return errors.New("apply integration candidate: finalization conflict receipt differs")
		}
		for _, outcome := range []string{"applied", "rebased"} {
			if err := registry.requireIntegrationReceiptAbsent(
				ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, original),
			); err != nil {
				return errors.New("apply integration candidate: original completion receipt is contradictory")
			}
		}
		for _, outcome := range []string{"target", "conflicted"} {
			if err := registry.requireIntegrationReceiptAbsent(
				ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, request),
			); err != nil {
				return errors.New("apply integration candidate: recovery authority receipt is contradictory")
			}
		}
	}
	if err := registry.requireIntegrationReceiptAbsent(
		ctx, request.Target.WorktreePath, integrationReceiptRef("applied", request),
	); err != nil {
		return errors.New("apply integration candidate: applied receipt is premature")
	}
	rebased, err := registry.inspectIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationReceiptRef("rebased", request),
	)
	if err != nil || rebased.kind != integrationReceiptAbsent &&
		(rebased.kind != integrationReceiptDirect || rebased.value != resultingHead) {
		return errors.New("apply integration candidate: rebased receipt differs")
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

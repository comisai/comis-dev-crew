package git

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type integrationReceiptKind uint8

const (
	integrationReceiptAbsent integrationReceiptKind = iota
	integrationReceiptDirect
	integrationReceiptSymbolic
)

type inspectedIntegrationReceipt struct {
	kind  integrationReceiptKind
	value string
}

func (registry *Registry) validateAppliedIntegrationReceiptFamily(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	head string,
) error {
	worktree := request.Target.WorktreePath
	if err := registry.requireDirectIntegrationReceipt(
		ctx, worktree, integrationReceiptRef("applied", request), head,
	); err != nil {
		return errors.New("apply integration candidate: applied receipt differs")
	}
	if request.RecoveryOperationID == "" {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef("conflicted", request),
		); err != nil {
			return errors.New("apply integration candidate: applied receipt family is contradictory")
		}
		if request.Strategy == application.IntegrationRebase {
			if err := registry.requireSymbolicIntegrationReceipt(
				ctx, worktree, integrationReceiptRef("target", request),
				"refs/heads/"+expectedIntegrationTargetBranch(request),
			); err != nil {
				return errors.New("apply integration candidate: applied target receipt differs")
			}
			if err := registry.requireDirectIntegrationReceipt(
				ctx, worktree, integrationReceiptRef("rebased", request), head,
			); err != nil {
				return errors.New("apply integration candidate: rebased receipt differs")
			}
			return nil
		}
		for _, outcome := range []string{"target", "rebased"} {
			if err := registry.requireIntegrationReceiptAbsent(
				ctx, worktree, integrationReceiptRef(outcome, request),
			); err != nil {
				return errors.New("apply integration candidate: applied receipt family is contradictory")
			}
		}
		return nil
	}
	original := originalIntegrationRequest(request)
	for _, outcome := range []string{"target", "conflicted"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef(outcome, request),
		); err != nil {
			return errors.New("apply integration candidate: recovery receipt family is contradictory")
		}
	}
	for _, outcome := range []string{"applied", "rebased"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef(outcome, original),
		); err != nil {
			return errors.New("apply integration candidate: original completion receipt is contradictory")
		}
	}
	if request.PendingMaterializationRecovery {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef("conflicted", original),
		); err != nil {
			return errors.New("apply integration candidate: original conflict receipt is contradictory")
		}
	} else if err := registry.requireDirectIntegrationReceipt(
		ctx, worktree, integrationReceiptRef("conflicted", original), request.Target.ExpectedHead,
	); err != nil {
		return errors.New("apply integration candidate: original conflict receipt differs")
	}
	if request.Strategy == application.IntegrationRebase {
		if err := registry.requireSymbolicIntegrationReceipt(
			ctx, worktree, integrationReceiptRef("target", original),
			"refs/heads/"+expectedIntegrationTargetBranch(request),
		); err != nil {
			return errors.New("apply integration candidate: original target receipt differs")
		}
		if err := registry.requireDirectIntegrationReceipt(
			ctx, worktree, integrationReceiptRef("rebased", request), head,
		); err != nil {
			return errors.New("apply integration candidate: recovered rebase receipt differs")
		}
		return nil
	}
	if err := registry.requireIntegrationReceiptAbsent(
		ctx, worktree, integrationReceiptRef("target", original),
	); err != nil {
		return errors.New("apply integration candidate: original target receipt is contradictory")
	}
	if err := registry.requireIntegrationReceiptAbsent(
		ctx, worktree, integrationReceiptRef("rebased", request),
	); err != nil {
		return errors.New("apply integration candidate: recovery rebase receipt is contradictory")
	}
	return nil
}

func (registry *Registry) validateConflictedIntegrationReceiptFamily(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	worktree := request.Target.WorktreePath
	if err := registry.requireDirectIntegrationReceipt(
		ctx, worktree, integrationReceiptRef("conflicted", request), request.Target.ExpectedHead,
	); err != nil {
		return errors.New("apply integration candidate: conflict receipt differs")
	}
	for _, outcome := range []string{"applied", "rebased"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef(outcome, request),
		); err != nil {
			return errors.New("apply integration candidate: conflict receipt family is contradictory")
		}
	}
	if request.Strategy == application.IntegrationRebase {
		if err := registry.requireSymbolicIntegrationReceipt(
			ctx, worktree, integrationReceiptRef("target", request),
			"refs/heads/"+expectedIntegrationTargetBranch(request),
		); err != nil {
			return errors.New("apply integration candidate: conflict target receipt differs")
		}
		return nil
	}
	if err := registry.requireIntegrationReceiptAbsent(
		ctx, worktree, integrationReceiptRef("target", request),
	); err != nil {
		return errors.New("apply integration candidate: conflict target receipt is contradictory")
	}
	return nil
}

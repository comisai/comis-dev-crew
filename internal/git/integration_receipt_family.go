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
	snapshot, err := registry.integrationReceiptFamilySnapshot(ctx, request)
	if err != nil {
		return err
	}
	require := func(identity application.IntegrationAdapterRequest, outcome string, kind integrationReceiptKind, value string) error {
		return requireSnapshotReceipt(snapshot, integrationReceiptRef(outcome, identity), kind, value)
	}
	if requireSnapshotReceipt(snapshot, integrationRebaseProofRef(request), integrationReceiptAbsent, "") != nil ||
		require(request, "applied", integrationReceiptDirect, head) != nil {
		return errors.New("apply integration candidate: applied receipt family differs")
	}
	if request.RecoveryOperationID == "" {
		if require(request, "conflicted", integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: applied receipt family is contradictory")
		}
		if request.Strategy == application.IntegrationRebase {
			if require(request, "target", integrationReceiptSymbolic,
				"refs/heads/"+expectedIntegrationTargetBranch(request)) != nil ||
				require(request, "rebased", integrationReceiptDirect, head) != nil {
				return errors.New("apply integration candidate: applied rebase receipts differ")
			}
			return nil
		}
		for _, outcome := range []string{"target", "rebased"} {
			if require(request, outcome, integrationReceiptAbsent, "") != nil {
				return errors.New("apply integration candidate: applied receipt family is contradictory")
			}
		}
		return nil
	}
	original := originalIntegrationRequest(request)
	for _, outcome := range []string{"target", "conflicted"} {
		if require(request, outcome, integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: recovery receipt family is contradictory")
		}
	}
	for _, outcome := range []string{"applied", "rebased"} {
		if require(original, outcome, integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: original completion receipt is contradictory")
		}
	}
	conflictKind, conflictHead := integrationReceiptDirect, request.Target.ExpectedHead
	if request.PendingMaterializationRecovery {
		conflictKind, conflictHead = integrationReceiptAbsent, ""
	}
	if require(original, "conflicted", conflictKind, conflictHead) != nil {
		return errors.New("apply integration candidate: original conflict receipt differs")
	}
	if request.Strategy == application.IntegrationRebase {
		if require(original, "target", integrationReceiptSymbolic,
			"refs/heads/"+expectedIntegrationTargetBranch(request)) != nil ||
			require(request, "rebased", integrationReceiptDirect, head) != nil {
			return errors.New("apply integration candidate: recovered rebase receipts differ")
		}
		return nil
	}
	if require(original, "target", integrationReceiptAbsent, "") != nil ||
		require(request, "rebased", integrationReceiptAbsent, "") != nil {
		return errors.New("apply integration candidate: recovery receipt family is contradictory")
	}
	return nil
}

func (registry *Registry) validateCompletedIntegrationReceiptFamily(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	snapshot, err := registry.integrationReceiptFamilySnapshot(ctx, request)
	if err != nil {
		return err
	}
	for _, reference := range integrationReceiptFamilyReferences(request) {
		if requireSnapshotReceipt(snapshot, reference, integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: completed receipt family is contradictory")
		}
	}
	return nil
}

func (registry *Registry) validateConflictedIntegrationReceiptFamily(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	snapshot, err := registry.integrationReceiptFamilySnapshot(ctx, request)
	if err != nil {
		return err
	}
	require := func(outcome string, kind integrationReceiptKind, value string) error {
		return requireSnapshotReceipt(snapshot, integrationReceiptRef(outcome, request), kind, value)
	}
	if require("conflicted", integrationReceiptDirect, request.Target.ExpectedHead) != nil {
		return errors.New("apply integration candidate: conflict receipt differs")
	}
	for _, outcome := range []string{"applied", "rebased"} {
		if require(outcome, integrationReceiptAbsent, "") != nil {
			return errors.New("apply integration candidate: conflict receipt family is contradictory")
		}
	}
	if request.Strategy == application.IntegrationRebase {
		if require("target", integrationReceiptSymbolic,
			"refs/heads/"+expectedIntegrationTargetBranch(request)) != nil ||
			requireSnapshotReceipt(snapshot, integrationRebaseProofRef(request),
				integrationReceiptDirect, request.Candidate.HeadRevision) != nil {
			return errors.New("apply integration candidate: conflict rebase receipts differ")
		}
		return nil
	}
	if require("target", integrationReceiptAbsent, "") != nil ||
		requireSnapshotReceipt(snapshot, integrationRebaseProofRef(request), integrationReceiptAbsent, "") != nil {
		return errors.New("apply integration candidate: conflict receipt family is contradictory")
	}
	return nil
}

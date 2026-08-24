# Review evidence and threat boundaries

## Interrupted rebase recovery

The recovery regression can be reproduced without rewriting history. Apply the
`internal/git/integration_rebase_recovery_test.go` test-only diff from
`813cbf566d0ac2ff3d2feeef9b7db344892d1f90` to its immutable pre-fix parent
`ca1e697c1d30fe163eceac4ecaddae798b832f24`, then run:

```text
go test ./internal/git -run TestRegistry_ReconcilesInterruptedRebaseConflictBeforeReceipt -count=1
```

The pre-fix result is RED:

```text
ApplyIntegrationCandidate(interrupted conflict) = application.IntegrationAdapterResult{Outcome:"", PreviousHead:"", ResultingHead:"", ConflictPaths:[]string(nil)}, apply integration candidate: target worktree is unavailable
```

The implementation begins at `813cbf566d0ac2ff3d2feeef9b7db344892d1f90`.
The same named command is the focused GREEN replay on the final tree.

The dangling symbolic-receipt fix is
`8c1132fa8d5e763fa46fdc77bc2b539cf66a3080`; its RED command was:

```text
go test ./internal/git -run "TestRegistry_(ReconcilesCompletedRecoveryBeforeRebasedReceipt|RebaseTargetReceiptRejectsAlteredOrAmbiguousIdentity)" -count=1
```

Its `non-branch_symbolic_identity` case returned no error before the fail-closed
receipt inspection landed. Every target, rebased, conflict, and applied receipt
is now inspected without symbolic recursion and malformed, dangling, multi-hop,
altered, or unexpected identities remain unknown.

## Membership migration memory

Apply `internal/store/sqlite/initiative_membership_memory_test.go` to immutable
pre-streaming commit `38404408c1832f0c2fb19110b44367bf5755bd06`,
then run:

```text
go test ./internal/store/sqlite -run TestInitiativeMembershipMigrationBoundsLiveHeap -count=1
```

The pre-fix migration retained the whole initiative history and reached
32,597,320 bytes of live heap growth against the 12 MiB bound. The streaming
implementation begins at `f30f2781f76eff7c99c473259c68c5e75044bc38`;
it validates and backfills pages of 64.

## Authority and resource threats

- Integration reservation requires the exact `integrates_after` edge from the
  supplied candidate to the destination owner, every predecessor must satisfy
  dependency readiness, and only the durable `ready` owner posture permits a
  mutation or conflict continuation.
- Integration rejects repository configuration and attributes capable of
  launching hooks, merge drivers, filters, diff commands, editors, credentials,
  or file-system monitors. Rebase preflight uses the real rebase engine in an
  isolated repository before any target receipt or worktree mutation.
- Scheduling persists one priority head per round, repository, and worker
  profile. A capped repository therefore contributes a bounded resource head,
  not an unbounded history scan, while the global priority index still chooses
  the oldest eligible task.
- Missing, malformed, stale, contradictory, or incomplete graph, Git, migration,
  or scheduling evidence refuses mutation and preserves work.

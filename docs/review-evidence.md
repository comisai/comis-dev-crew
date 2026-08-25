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
- Integration inspects local and worktree Git configuration without includes
  before status or mutation. It rejects hooks, merge drivers, filters, diff
  commands, editors, credential helpers, and file-system monitors, as well as
  unsafe attributes.
- The real merge, rebase, or cherry-pick engine runs in an isolated
  service-owned repository. Successful result objects and their exact semantic
  proof are persisted before the shared worktree consumes them. Isolated
  conflicts refuse without shared mutation. Loose objects are content-validated
  and copied independently onto the destination filesystem before atomic
  publication.
- Shared result adoption persists the expected index, expected and result trees,
  result proof, and pending transition before the target compare-and-swap. Only
  an unchanged expected worktree can then be safely materialized; a crash after
  the compare-and-swap resumes from that transition, while developer edits and
  divergent refs remain untouched.
- Recovery validates the complete original and recovery receipt set through
  non-recursive tri-state inspection. A completed result can be reconciled
  after the target compare-and-swap. Every completion posture rechecks the
  deadline and state-specific receipt set before its next ref, index, or
  worktree mutation.
- Pre-mutation policy and topology refusals settle as `aborted`, distinct from
  candidate evidence invalidation, so the exact reservation is released
  without claiming the evidence changed. An aborted recovery releases conflict
  exclusivity for a corrected operation while the same operation still replays
  its terminal receipt.
- Scheduling persists one priority head per round, repository, and worker
  profile and advances that resource's indexed frontier for every available
  slot. Capped resources therefore cannot cause an unbounded priority scan,
  while global ordering still chooses the oldest eligible task.
- Missing, malformed, stale, contradictory, or incomplete graph, Git, migration,
  or scheduling evidence refuses mutation and preserves work.
- Capacity deferral never replaces a requested task's dependency, contract, or
  integration blocker with `resource_queued`.

The Round 21 behavioral regressions are preserved in test-only commit
`ed472b56765db9f071aa0b8477846c7a68fd69de`. The exact RED commands were:

```text
go test ./internal/git -run 'TestRegistry_(RejectsWorktreeFSMonitorBeforeStatus|RechecksEvidenceImmediatelyBeforeEveryInitialMutation|RechecksEvidenceBeforeRebaseContinuation|IsolatedRebaseDoesNotUpdateUnrelatedRefs|ReconcilesCleanRebaseCrashBeforeProofCompletion|RebaseRecoveryRejectsRewrittenResolvedCommitAfterCrash|CherryPickRefusesPartialRangeBeforeTargetMutation|RebasePreflightCleansTrackedSymlinkWorkspace|RecoveryRejectsEveryUnexpectedCompletionReceipt)' -count=1
go test ./internal/application -run TestIntegrationSettlesPreconditionRefusalsWithoutInvalidatingEvidence -count=1
go test ./internal/store/sqlite -run 'Test(InitiativeLaunchAuthorizationAdvancesWithinResourceForEverySlot|IntegrationAbortedSettlementPreservesCandidateEvidence)' -count=1
```

Before the implementation, the focused Git command reported that a
worktree `core.fsmonitor` marker executed, expired merge, cherry-pick, and
recovery operations returned no error, an unrelated ref moved during rebase,
clean rebase crash recovery lacked a server proof, a rewritten continued commit
was accepted, cherry-pick partially mutated the target, tracked-symlink cleanup
failed, and unexpected recovery receipts were accepted. The focused application
and SQLite commands also observed `invalidated` instead of `aborted` and queued
the second older same-resource task behind later work.

The Round 22 behavioral regressions are preserved in test-only commit
`ffc45417db2866df860a444bcc541cf21334d41c`. The exact RED commands were:

```text
go test ./internal/git -run 'TestRegistry_(CompletedInitialRebaseRechecksDeadlineBeforeMutation|CompletedRecoveryRechecksEveryReceiptAndDeadline)$' -count=1
go test ./internal/git -run 'TestRegistry_(MaterializationPreservesEditsAcrossCASFailures|ReconcilesCrashAfterResultCAS)$' -count=1
go test ./internal/git -run '^TestRegistry_IsolatedMergeConflictRefusesSharedMutation$' -count=1
go test ./internal/git -run '^TestImportIsolatedGitObjectsCopiesAndValidatesLooseObjects$' -count=1
go test ./internal/store/sqlite -run '^TestIntegrationAbortedRecoveryReleasesConflictForCorrectedOperation$' -count=1
go test ./internal/store/sqlite -run '^TestInitiativeLaunchAuthorizationPreservesRequestedTaskBlockerWhenCapacityFills$' -count=1
```

Before implementation, completed initial and recovery rebases succeeded with an
expired deadline or a dangling original completion receipt; failed result CAS
boundaries overwrote developer edits; a post-CAS retry remained stranded; an
isolated merge conflict was rerun in the shared worktree; aborted recovery still
blocked a corrected operation; imported objects shared source inodes and corrupt
objects were accepted; and a dependency blocker was reported as capacity.

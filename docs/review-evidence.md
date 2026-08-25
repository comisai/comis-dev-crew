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

The distinct completed-before-receipt regression was reconstructed by applying
`TestRegistry_ReconcilesCompletedRebaseBeforeConflictReceipt` from immutable
test commit `5f68a2e03c8ef6f07478c6d913c3856f5b494e83`, together with its inert Git
fixture helper, to the implementation's immutable pre-fix parent
`ca1e697c1d30fe163eceac4ecaddae798b832f24`. Its exact command was:

```text
go test ./internal/git -run '^TestRegistry_ReconcilesCompletedRebaseBeforeConflictReceipt$' -count=1
```

The pre-fix result was RED:

```text
ApplyIntegrationCandidate(completed before receipt) = application.IntegrationAdapterResult{Outcome:"", PreviousHead:"", ResultingHead:"", ConflictPaths:[]string(nil)}, apply integration candidate: target worktree is unavailable
```

Applying the complete test-only diff to implementation commit
`813cbf566d0ac2ff3d2feeef9b7db344892d1f90` and running the same exact command
returned GREEN:

```text
ok github.com/comisai/comis-dev-crew/internal/git
```

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
  before mutation. Candidate cleanliness and operator diff summaries use raw
  commit trees, index trees, bounded blob reads, and rooted no-follow worktree
  comparison rather than Git status or content diff in the worker-controlled
  repository. Dynamically named filters, text converters, external diff
  commands, info attributes, and configuration races therefore cannot execute
  during candidate inspection. Repository-aware Git children still receive
  fixed service-owned configuration; the generic bounded process seam receives
  its caller's exact argument vector.
- The real merge, rebase, or cherry-pick engine runs in an isolated
  service-owned repository. Successful result objects and their exact semantic
  proof are persisted before the shared worktree consumes them. Isolated
  conflicts for every strategy refuse without proof-ref, receipt, index,
  worktree, or target-ref mutation. Automatic Git maintenance and object
  packing are disabled in every isolated engine. The complete loose-object set
  is validated before publication with closed object-count, per-object,
  aggregate compressed, aggregate decompressed, and result-tree bounds. Loose
  objects are copied independently onto the destination filesystem only after
  that bounded plan succeeds;
  a rooted directory handle preserves the validated object-database identity
  through exclusive temporary creation, fsync, collision checks, and atomic
  publication even if its ambient path is replaced.
- Shared result adoption persists the expected index, expected and result trees,
  result proof, and pending transition before the target compare-and-swap. Only
  a completely representable result tree and an unchanged expected worktree can
  then be safely materialized. Tree entry, per-blob, and aggregate bounds are
  enforced before compare-and-swap. Untrusted regular files are compared through
  rooted no-follow handles with size-first bounded streaming and replacement
  detection. Existing regular files are journaled and rewritten through their
  identity-checked authoritative inode, so writes through an already-open
  developer descriptor remain visible in the worktree and are detected before
  recovery evidence can retire. Additions use atomic no-replace publication;
  deletions and existing-entry type changes refuse before target publication.
  Racing developer entries and service stages are preserved for exact restart
  reconciliation. No post-CAS Git
  checkout consumes mutable repository configuration or info attributes, so a
  racing dynamically named filter cannot execute with service authority. A
  crash after the compare-and-swap resumes from that transition, while partial
  writes, developer edits, and divergent refs remain untouched and unknown.
  Evidence freshness and strategy-specific receipts are reauthorized
  immediately before post-CAS index/worktree materialization; expiry preserves
  the pending transition and expected worktree for a later authorized retry.
  Fresh merge and cherry-pick materialization validates the complete closed
  original/recovery receipt and proof family before target, index, and worktree
  mutation, after each mutation boundary, and around terminal applied-receipt
  publication. Any dangling, symbolic, altered, or contradictory sibling keeps
  the durable transition unknown instead of returning success.
- Prepared rebase and conflict-recovery restoration publish immutable source,
  target, tree, index, branch, and proof identity before the first index,
  worktree, or HEAD mutation. Restart accepts only the closed original,
  index-restored, worktree-restored, or reattached posture and resumes the next
  authorized step; every contradictory partial state preserves work and refuses.
- Recovery validates the complete original and recovery receipt set through
  non-recursive tri-state inspection. A completed result can be reconciled
  after the target compare-and-swap. Every completion posture rechecks the
  deadline and state-specific receipt set before its next ref, index, or
  worktree mutation.
- An expired post-compare-and-swap transition remains pending until a distinct
  store-authorized operation revalidates current evidence, writer exclusion,
  the immutable original transition, the advanced target, and the unchanged
  expected index/worktree. A pristine published plan or rebase proof with no
  transition or receipt is positively settled as mutation-not-started.
- Rebase recovery validates a rooted, regular-file sequencer whose
  completed and remaining picks, stopped commit, target/original heads, proof
  branch, and counters exactly match the server proof. Executable, ref-updating,
  dropped, reordered, extra, unknown, symbolic, or dangling metadata refuses
  before recovery. The validated shared sequencer is never executed: the
  resolved tree and remaining ordered commits complete in a new service-owned
  isolated engine, and only the proved result enters the durable
  compare-and-swap/materialization transition.
- Mutable policy refusal first classifies the complete receipt, proof,
  transition, target, worktree, and sequencer posture. Any mutation evidence
  preserves reconciliation authority and cannot be mislabeled as an aborted
  pre-mutation attempt. Terminal and completed replay validates every original
  and recovery sibling receipt plus the operation proof ref through the shared
  non-recursive tri-state boundary before accepting the durable outcome.
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

The Round 23 behavioral regressions are preserved in test-only commit
`2d999ca4a6c545e56c0134706a813059fbcd5ae4`. The exact RED command was:

```text
go test ./internal/git -run 'TestRegistry_(RefusesNewIsolatedRebaseConflictBeforeSharedMutation|PostCASMaterializationRequiresFreshAuthorization|ReceiptOnlyReconcilesCompletedMaterializationBeforeRebasedReceipt|IsolatedEnginesDisableAutomaticObjectPacking)|TestImportIsolatedGitObjectsHoldsDestinationAcrossSymlinkSwap' -count=1
```

Before implementation, a new isolated rebase conflict returned success,
post-CAS expiry still materialized the result, receipt-only recovery required a
missing rebased receipt despite an exact completed transition, destination-path
replacement interrupted object import, and automatic isolated object packing
made valid imports fail.

The Round 24 behavioral regressions are preserved in test-only commit
`4bb319fed4fe5af9a5ef5fc946af0626e25c3848`. The exact RED command was:

```text
go test ./internal/git ./internal/store/sqlite -run 'TestRegistry_(FreshOperationAdoptsExactPendingMaterialization|FreshPendingMaterializationNeverOverwritesEdits|ReceiptOnlySettlesPristinePublishedPlan|ReceiptOnlySettlesPristinePublishedRebaseProof|RebaseRecoveryRejectsAlteredSequencerBeforeContinue|RebaseRecoveryRejectsReorderedRemainingCommits)|TestPendingIntegrationCanBeResumedByFreshAuthorizedOperation' -count=1
```

Before implementation, fresh pending recovery was rejected by both the Git and
store boundaries, pristine published plans and rebase proofs could not settle,
an injected sequencer `exec` directive ran, `update-ref` was accepted, and a
reordered remaining sequence advanced past the protected conflict.
The same named regressions are GREEN on the fixed tree:

```text
ok github.com/comisai/comis-dev-crew/internal/git
ok github.com/comisai/comis-dev-crew/internal/store/sqlite
```

The Round 25 behavioral regressions are preserved in test-only commit
`bac6ebcdc43d9467e304efd7442a116fedfabbd8`. The exact RED commands were:

```text
go test ./internal/git -run 'TestRegistry_(MutatedReplayPolicyRefusalIsNeverPreMutation|AppliedReplayRejectsContradictorySiblingReceipts|CandidateInspectionIgnoresRacingFSMonitor|RebaseRecoveryNeverConsumesReplacedSharedSequencer|PendingRecoverySettlesAfterPostMaterializationExpiry)' -count=1
go test ./internal/git -run 'TestRegistry_PendingRecoverySettlesAfterPostMaterializationExpiry' -count=1
```

Before implementation, mutated applied and pending replays returned the
mutation-not-started marker, contradictory sibling receipts replayed as
applied, a racing file-system monitor executed, shared sequencer replacement
reached `rebase --continue`, and post-materialization expiry stranded merge,
cherry-pick, and rebase recovery instead of settling their exact results.
The Round 25 regressions and the existing successful recovery contracts are
GREEN on the fixed tree:

```text
go test ./internal/git -run 'TestRegistry_(MutatedReplayPolicyRefusalIsNeverPreMutation|AppliedReplayRejectsContradictorySiblingReceipts|CandidateInspectionIgnoresRacingFSMonitor|RebaseRecoveryNeverConsumesReplacedSharedSequencer|PendingRecoverySettlesAfterPostMaterializationExpiry|RecoversResolvedRebaseConflictAndReattachesExactTarget|ReconcilesCompletedRecoveryBeforeRebasedReceipt|ReceiptOnlyRecoveryRequiresOriginalReceiptAuthority)' -count=1
ok github.com/comisai/comis-dev-crew/internal/git 91.920s
```

The Round 26 behavioral regressions are preserved in test-only commit
`06a9e204fa766e563f10c26cc525dc83dc6b99d1`. The exact RED command was:

```text
go test ./internal/git -run 'TestRegistry_(CompletedMaterializationRejectsContradictoryReceiptFamily|AppliedReplayRejectsUnexpectedProofRef|MaterializationIgnoresRacingDynamicFilter)' -count=1
```

Before implementation, completed merge and cherry-pick transitions returned
success with contradictory target or rebased receipts, terminal merge,
cherry-pick, and rebase replays accepted a resurrected proof ref, and a racing
dynamically named smudge filter executed during post-CAS materialization.
Test-only commit `ae8ff12817f27a0f90fc92df7e66fbbca32ec8a3`
strengthens the same executable race with a dynamic process filter, which also
executed before integration cleanliness inspection moved to immutable tree,
index, and rooted worktree comparison.

The focused GREEN command on the fixed tree is:

```text
go test ./internal/git -run 'TestRegistry_(CompletedMaterializationRejectsContradictoryReceiptFamily|AppliedReplayRejectsUnexpectedProofRef|MaterializationIgnoresRacingDynamicFilter|MaterializationPreservesEditsAcrossCASFailures|ReconcilesCrashAfterResultCAS|FreshOperationAdoptsExactPendingMaterialization|FreshPendingMaterializationNeverOverwritesEdits|PostCASMaterializationRequiresFreshAuthorization|PendingRecoverySettlesAfterPostMaterializationExpiry|RebasePreflightCleansTrackedSymlinkWorkspace|ReceiptOnlyReconcilesCompletedMaterializationBeforeRebasedReceipt|AppliedReplayRejectsContradictorySiblingReceipts|CandidateInspectionIgnoresRacingFSMonitor)' -count=1
ok github.com/comisai/comis-dev-crew/internal/git 76.011s
```

## Round 29 materialization and inspection authority

The five behavioral RED slices are preserved independently:

- `b5ed7ea` proves writes through open tracked descriptors disappeared at both
  publication boundaries while materialization returned success.
- `747e35f` proves fresh merge and cherry-pick materialization returned applied
  after racing dangling sibling receipts at index and terminal boundaries.
- `8219592` proves a racing dynamically named filter process executed during
  candidate inspection.
- `14ca2d5` proves oversized and excessive isolated loose-object sets were
  published without a pre-publication resource refusal.
- `67b71a1` proves the generic bounded child seam received Git configuration
  arguments instead of its exact caller-supplied vector.

The exact RED commands were:

```text
go test ./internal/git -run '^TestMaterializationPreservesWritesThroughOpenTrackedDescriptor$' -count=1
go test ./internal/git -run '^TestRegistry_FreshMaterializationRejectsRacingReceiptFamily$' -count=1
go test ./internal/git -run '^TestRegistry_Candidate(InspectionIgnoresRacingDynamicFilterProcess|DiffIgnoresDynamicTextConversionDriver)$' -count=1
go test ./internal/git -run '^TestImportIsolatedGitObjectsRejectsOversizedObjectBeforePublication$' -count=1
go test ./internal/git -run '^TestBoundedChildProcessPreservesExactArguments$' -count=1
```

The focused GREEN command covers those regressions plus their adjacent public
candidate-diff, runner, import, crash-replay, and receipt-family contracts:

```text
go test ./internal/git -run 'Test(MaterializationPreservesWritesThroughOpenTrackedDescriptor|Registry_FreshMaterializationRejectsRacingReceiptFamily|Registry_CandidateInspectionIgnoresRacingDynamicFilterProcess|Registry_CandidateDiffIgnoresDynamicTextConversionDriver|ImportIsolatedGitObjects|BoundedChildProcessPreservesExactArguments|GitInspectionRunner_IsBoundedCancellableAndContentFreeOnFailure|WorkspaceGitRunner_PropagatesEnvironmentAndNormalizesCommandOutcomes|Registry_InspectCandidateDiff|Registry_AppliesEveryReviewedIntegrationStrategyAndReplays|MaterializationRetryRetiresCrashRecoveryEvidence)' -count=1
ok github.com/comisai/comis-dev-crew/internal/git 42.800s
```

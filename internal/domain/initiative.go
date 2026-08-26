package domain

import (
	"sort"
	"time"
)

// InitiativeState is the closed initiative lifecycle. Unknown is durable: it
// records that the service cannot currently describe the initiative, which is
// not the same as idle and not the same as failed.
type InitiativeState string

// MaximumInitiativeMembers is the shared group and initiative member bound.
const MaximumInitiativeMembers = 16

const (
	InitiativePreparing         InitiativeState = "preparing"
	InitiativeActive            InitiativeState = "active"
	InitiativeBlocked           InitiativeState = "blocked"
	InitiativeIntegrating       InitiativeState = "integrating"
	InitiativeValidating        InitiativeState = "validating"
	InitiativeCandidateComplete InitiativeState = "candidate_complete"
	InitiativeDelivered         InitiativeState = "delivered"
	InitiativeFailed            InitiativeState = "failed"
	InitiativeCancelled         InitiativeState = "cancelled"
	InitiativeUnknown           InitiativeState = "unknown"
)

func (state InitiativeState) valid() bool {
	switch state {
	case InitiativePreparing, InitiativeActive, InitiativeBlocked, InitiativeIntegrating,
		InitiativeValidating, InitiativeCandidateComplete, InitiativeDelivered,
		InitiativeFailed, InitiativeCancelled, InitiativeUnknown:
		return true
	}
	return false
}

// InitiativeEdgeKind is the closed dependency vocabulary. Each kind states WHY
// one task waits for another, because a scheduler that only knows "waits" cannot
// tell an operator which lanes a failure actually blocks.
type InitiativeEdgeKind string

const (
	EdgeBlocksStart      InitiativeEdgeKind = "blocks_start"
	EdgeBlocksValidation InitiativeEdgeKind = "blocks_validation"
	EdgeConsumesArtifact InitiativeEdgeKind = "consumes_artifact"
	EdgeIntegratesAfter  InitiativeEdgeKind = "integrates_after"
)

func (kind InitiativeEdgeKind) valid() bool {
	switch kind {
	case EdgeBlocksStart, EdgeBlocksValidation, EdgeConsumesArtifact, EdgeIntegratesAfter:
		return true
	}
	return false
}

// blocksStart reports whether an edge gates the downstream task's launch rather
// than only its validation. A validation edge lets the consumer work while its
// producer runs; only its evidence has to wait.
func (kind InitiativeEdgeKind) blocksStart() bool {
	return kind == EdgeBlocksStart || kind == EdgeConsumesArtifact || kind == EdgeIntegratesAfter
}

// ContractArtifactKind is the closed contract vocabulary. It grows only with a
// concrete producer and a concrete consumer.
type ContractArtifactKind string

const (
	ArtifactAPISchema         ContractArtifactKind = "api_schema"
	ArtifactGeneratedClient   ContractArtifactKind = "generated_client"
	ArtifactFixture           ContractArtifactKind = "fixture"
	ArtifactMigrationContract ContractArtifactKind = "migration_contract"
	ArtifactIntegrationNote   ContractArtifactKind = "integration_note"
)

func (kind ContractArtifactKind) valid() bool {
	switch kind {
	case ArtifactAPISchema, ArtifactGeneratedClient, ArtifactFixture,
		ArtifactMigrationContract, ArtifactIntegrationNote:
		return true
	}
	return false
}

// InitiativeBaseRevision freezes one revision per repository. Freezing is what
// lets a worker's evidence stay meaningful: without it a worker could rebase
// onto a moving default branch and still call its old result current.
type InitiativeBaseRevision struct {
	RepositoryID string `json:"repositoryId"`
	Revision     string `json:"revision"`
}

// InitiativeComponent groups the tasks that carry one responsibility. The
// responsibility text itself is domain content and stays private to the
// companion; only the reference travels.
type InitiativeComponent struct {
	ComponentHandle   string   `json:"componentHandle"`
	RepositoryID      string   `json:"repositoryId"`
	ResponsibilityRef string   `json:"responsibilityRef"`
	TaskHandles       []string `json:"taskHandles"`
}

// InitiativeEdge is one dependency at the current initiative revision.
type InitiativeEdge struct {
	FromTaskHandle       string               `json:"fromTaskHandle"`
	ToTaskHandle         string               `json:"toTaskHandle"`
	Kind                 InitiativeEdgeKind   `json:"kind"`
	RequiredArtifactKind ContractArtifactKind `json:"requiredArtifactKind,omitempty"`
}

// DevelopmentInitiative coordinates several components as one durable unit.
//
// The graph is closed and acyclic at every revision, and every edge names two
// tasks this initiative already contains. Those two rules together are what keep
// an initiative from becoming a general workflow engine reaching across
// authorities: a dependency can only ever be expressed between members.
type DevelopmentInitiative struct {
	SchemaVersion        int                      `json:"schemaVersion"`
	Handle               string                   `json:"handle"`
	ManagedRunGroupID    string                   `json:"managedRunGroupId,omitempty"`
	TitleRef             string                   `json:"titleRef"`
	State                InitiativeState          `json:"state"`
	BaseRevisionSet      []InitiativeBaseRevision `json:"baseRevisionSet"`
	Components           []InitiativeComponent    `json:"components"`
	Edges                []InitiativeEdge         `json:"edges"`
	ContractArtifacts    []string                 `json:"contractArtifacts"`
	IntegrationPolicyID  string                   `json:"integrationPolicyId"`
	IntegrationOwnerTask string                   `json:"integrationOwnerTask,omitempty"`
	StateVersion         int64                    `json:"stateVersion"`
	CreatedAt            time.Time                `json:"createdAt"`
	UpdatedAt            time.Time                `json:"updatedAt"`
}

// Validate enforces the initiative record and its graph invariants.
func (initiative DevelopmentInitiative) Validate() error {
	if initiative.SchemaVersion != 1 {
		return &ValidationError{Field: "schemaVersion", Reason: "must equal 1"}
	}
	if err := validateOpaqueID("initiativeHandle", initiative.Handle); err != nil {
		return err
	}
	if err := validateOpaqueID("integrationPolicyId", initiative.IntegrationPolicyID); err != nil {
		return err
	}
	if err := validateBoundedSafeText("titleRef", initiative.TitleRef, 256); err != nil {
		return err
	}
	if initiative.ManagedRunGroupID == "" {
		if initiative.State != InitiativePreparing && initiative.State != InitiativeUnknown {
			return &ValidationError{
				Field:  "managedRunGroupId",
				Reason: "must be bound before an initiative becomes active or terminal",
			}
		}
	} else if err := validateAuthorityReference("managedRunGroupId", initiative.ManagedRunGroupID); err != nil {
		return err
	}
	if !initiative.State.valid() {
		return &ValidationError{Field: "state", Reason: "must be a closed initiative state"}
	}
	if initiative.StateVersion < 1 {
		return &ValidationError{Field: "stateVersion", Reason: "must be positive"}
	}
	if initiative.UpdatedAt.Before(initiative.CreatedAt) {
		return &ValidationError{Field: "updatedAt", Reason: "cannot precede creation"}
	}

	bases, err := initiative.validateBaseRevisions()
	if err != nil {
		return err
	}
	members, err := initiative.validateComponents(bases)
	if err != nil {
		return err
	}
	if err := initiative.validateEdges(members); err != nil {
		return err
	}
	if err := initiative.validateContractArtifacts(); err != nil {
		return err
	}
	if initiative.IntegrationOwnerTask != "" && !members[initiative.IntegrationOwnerTask] {
		return &ValidationError{
			Field:  "integrationOwnerTask",
			Reason: "must name a task this initiative contains",
		}
	}
	return nil
}

func (initiative DevelopmentInitiative) validateContractArtifacts() error {
	if len(initiative.ContractArtifacts) > 128 {
		return &ValidationError{Field: "contractArtifacts", Reason: "must hold at most 128 artifacts"}
	}
	seen := make(map[string]struct{}, len(initiative.ContractArtifacts))
	for _, handle := range initiative.ContractArtifacts {
		if err := validateOpaqueID("contractArtifacts", handle); err != nil {
			return err
		}
		if _, duplicate := seen[handle]; duplicate {
			return &ValidationError{Field: "contractArtifacts", Reason: "artifact handles must be unique"}
		}
		seen[handle] = struct{}{}
	}
	return nil
}

func (initiative DevelopmentInitiative) validateBaseRevisions() (map[string]struct{}, error) {
	if len(initiative.BaseRevisionSet) == 0 || len(initiative.BaseRevisionSet) > 32 {
		return nil, &ValidationError{Field: "baseRevisionSet", Reason: "must freeze between one and 32 repositories"}
	}
	bases := make(map[string]struct{}, len(initiative.BaseRevisionSet))
	for _, base := range initiative.BaseRevisionSet {
		if err := ValidateRepositoryID(base.RepositoryID); err != nil {
			return nil, err
		}
		if err := validateRevision(base.Revision); err != nil {
			return nil, err
		}
		if _, exists := bases[base.RepositoryID]; exists {
			// Two bases for one repository would let two components each call a
			// different revision "the" base and both claim their evidence current.
			return nil, &ValidationError{
				Field:  "baseRevisionSet",
				Reason: "must freeze exactly one revision per repository",
			}
		}
		bases[base.RepositoryID] = struct{}{}
	}
	return bases, nil
}

func (initiative DevelopmentInitiative) validateComponents(
	bases map[string]struct{},
) (map[string]bool, error) {
	if len(initiative.Components) == 0 || len(initiative.Components) > 64 {
		return nil, &ValidationError{Field: "components", Reason: "must hold between one and 64 components"}
	}
	handles := make(map[string]struct{}, len(initiative.Components))
	members := make(map[string]bool)
	for _, component := range initiative.Components {
		if err := validateOpaqueID("componentHandle", component.ComponentHandle); err != nil {
			return nil, err
		}
		if err := ValidateRepositoryID(component.RepositoryID); err != nil {
			return nil, err
		}
		if _, frozen := bases[component.RepositoryID]; !frozen {
			return nil, &ValidationError{
				Field:  "components.repositoryId",
				Reason: "every component repository must have a frozen base revision",
			}
		}
		if _, exists := handles[component.ComponentHandle]; exists {
			return nil, &ValidationError{Field: "components", Reason: "component handles must be unique"}
		}
		handles[component.ComponentHandle] = struct{}{}
		if len(component.TaskHandles) == 0 || len(component.TaskHandles) > 64 {
			return nil, &ValidationError{Field: "components.taskHandles", Reason: "must hold between one and 64 tasks"}
		}
		for _, handle := range component.TaskHandles {
			if err := ValidateTaskHandle(handle); err != nil {
				return nil, err
			}
			if members[handle] {
				// One task belongs to exactly one component. Sharing would make
				// "which component owns this failure" unanswerable.
				return nil, &ValidationError{
					Field:  "components.taskHandles",
					Reason: "a task belongs to exactly one component",
				}
			}
			members[handle] = true
		}
	}
	return members, nil
}

func (initiative DevelopmentInitiative) validateEdges(members map[string]bool) error {
	if len(initiative.Edges) > 512 {
		return &ValidationError{Field: "edges", Reason: "must hold at most 512 edges"}
	}
	type edgeKey struct {
		from string
		to   string
		kind InitiativeEdgeKind
	}
	seen := make(map[edgeKey]struct{}, len(initiative.Edges))
	for _, edge := range initiative.Edges {
		if !edge.Kind.valid() {
			return &ValidationError{Field: "edges.kind", Reason: "must be a closed edge kind"}
		}
		if !members[edge.FromTaskHandle] || !members[edge.ToTaskHandle] {
			// An edge naming a task outside this initiative is how a
			// cross-initiative dependency would enter the graph.
			return &ValidationError{
				Field:  "edges",
				Reason: "both endpoints must be tasks this initiative contains",
			}
		}
		if edge.FromTaskHandle == edge.ToTaskHandle {
			return &ValidationError{Field: "edges", Reason: "a task cannot depend on itself"}
		}
		if edge.Kind == EdgeConsumesArtifact {
			if !edge.RequiredArtifactKind.valid() {
				// A consumer that does not name what it consumes cannot be told
				// its contract went stale.
				return &ValidationError{
					Field:  "edges.requiredArtifactKind",
					Reason: "an artifact edge must name the artifact kind it consumes",
				}
			}
		} else if edge.RequiredArtifactKind != "" {
			return &ValidationError{
				Field:  "edges.requiredArtifactKind",
				Reason: "only an artifact edge may name a required artifact kind",
			}
		}
		key := edgeKey{from: edge.FromTaskHandle, to: edge.ToTaskHandle, kind: edge.Kind}
		if _, exists := seen[key]; exists {
			return &ValidationError{Field: "edges", Reason: "edges must be unique"}
		}
		seen[key] = struct{}{}
	}
	if initiative.hasCycle(members) {
		return &ValidationError{Field: "edges", Reason: "must form an acyclic graph"}
	}
	return nil
}

func (initiative DevelopmentInitiative) hasCycle(members map[string]bool) bool {
	adjacency := make(map[string][]string, len(members))
	for _, edge := range initiative.Edges {
		adjacency[edge.FromTaskHandle] = append(adjacency[edge.FromTaskHandle], edge.ToTaskHandle)
	}
	const (
		unvisited = 0
		onStack   = 1
		done      = 2
	)
	mark := make(map[string]int, len(members))
	var visit func(string) bool
	visit = func(node string) bool {
		mark[node] = onStack
		for _, next := range adjacency[node] {
			switch mark[next] {
			case onStack:
				return true
			case unvisited:
				if visit(next) {
					return true
				}
			}
		}
		mark[node] = done
		return false
	}
	for member := range members {
		if mark[member] == unvisited && visit(member) {
			return true
		}
	}
	return false
}

// DependencyReadyTasks lists the members whose launch-blocking dependencies are
// all satisfied, sorted for a stable projection.
//
// Parallelism is the default: sharing a repository is not a reason to wait,
// because every task receives its own worktree. Only a recorded edge serializes.
func (initiative DevelopmentInitiative) DependencyReadyTasks(satisfied map[string]bool) []string {
	blocked := make(map[string]bool)
	members := make(map[string]bool)
	for _, component := range initiative.Components {
		for _, handle := range component.TaskHandles {
			members[handle] = true
		}
	}
	for _, edge := range initiative.Edges {
		if edge.Kind.blocksStart() && !satisfied[edge.FromTaskHandle] {
			blocked[edge.ToTaskHandle] = true
		}
	}
	ready := make([]string, 0, len(members))
	for member := range members {
		if !blocked[member] && !satisfied[member] {
			ready = append(ready, member)
		}
	}
	sort.Strings(ready)
	return ready
}

// AuthorizeIntegrationWrite reports whether one task may apply candidates to the
// integration target.
//
// Exactly one member may, and only the member the initiative recorded. Component
// tasks publish candidate commits; they never receive the integration lease,
// because two writers applying to one target turn a merge race into a conflict
// nobody can attribute.
//
// An initiative with no recorded owner refuses everyone. Allowing any member
// through when the field is empty would silently make every member a writer,
// which is the opposite of what the record says.
func (initiative DevelopmentInitiative) AuthorizeIntegrationWrite(taskHandle string) error {
	if initiative.IntegrationOwnerTask == "" {
		return &ValidationError{
			Field:  "integrationOwnerTask",
			Reason: "this initiative records no integration owner, so no task may integrate",
		}
	}
	if taskHandle != initiative.IntegrationOwnerTask {
		return &ValidationError{
			Field:  "integrationOwnerTask",
			Reason: "only the recorded integration owner may apply candidates",
		}
	}
	return nil
}

// AuthorizeIntegrationWorktree proves the integration owner holds a worktree of
// its own.
//
// Sharing one with a component would let that component's uncommitted work
// appear inside an integration result without ever having been published as a
// candidate — which is exactly the provenance the integration lane exists to
// keep straight.
func (initiative DevelopmentInitiative) AuthorizeIntegrationWorktree(
	taskHandle string,
	worktreeByTask map[string]string,
) error {
	if err := initiative.AuthorizeIntegrationWrite(taskHandle); err != nil {
		return err
	}
	own, held := worktreeByTask[taskHandle]
	if !held || own == "" {
		return &ValidationError{
			Field:  "integrationWorktree",
			Reason: "the integration owner holds no worktree of its own",
		}
	}
	for otherTask, otherPath := range worktreeByTask {
		if otherTask != taskHandle && otherPath == own {
			return &ValidationError{
				Field:  "integrationWorktree",
				Reason: "the integration worktree must not be shared with a component task",
			}
		}
	}
	return nil
}

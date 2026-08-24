package domain_test

import (
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func artifactFixture() domain.ComponentContractArtifact {
	return domain.ComponentContractArtifact{
		ArtifactHandle:     "artifact-api-v1",
		InitiativeHandle:   "initiative-alpha",
		ProducerTaskHandle: "task-backend",
		Kind:               domain.ArtifactAPISchema,
		ContentHash:        "aa" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"[:62],
		SourceRevision:     "0123456789abcdef0123456789abcdef01234567",
		MediaType:          "application/json",
		Size:               128,
		ProducedAt:         time.Unix(1_800_000_000, 0).UTC(),
	}
}

func TestContractArtifactAcceptsAnImmutableDigestedArtifact(t *testing.T) {
	if err := artifactFixture().Validate(); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
}

func TestContractArtifactRequiresADigest(t *testing.T) {
	artifact := artifactFixture()
	// A mutable artifact without a digest is exactly what makes a downstream
	// brief unpinnable: the consumer could not tell that what it read changed.
	artifact.ContentHash = ""
	if err := artifact.Validate(); err == nil {
		t.Fatal("artifact without a digest accepted")
	}
}

func TestContractArtifactRejectsAnEmptyOrOversizedBody(t *testing.T) {
	artifact := artifactFixture()
	artifact.Size = 0
	if err := artifact.Validate(); err == nil {
		t.Fatal("empty artifact accepted")
	}
	artifact.Size = 1 << 30
	if err := artifact.Validate(); err == nil {
		t.Fatal("unbounded artifact accepted")
	}
}

func TestContractArtifactRequiresDurableProductionTime(t *testing.T) {
	artifact := artifactFixture()
	artifact.ProducedAt = time.Time{}
	if err := artifact.Validate(); err == nil {
		t.Fatal("artifact without a production time accepted")
	}
}

func TestContractArtifactCannotSupersedeItself(t *testing.T) {
	artifact := artifactFixture()
	artifact.SupersedesArtifactHandle = artifact.ArtifactHandle
	if err := artifact.Validate(); err == nil {
		t.Fatal("self-supersession accepted")
	}
}

func TestSupersessionStalesExactlyTheConsumersOfTheSupersededContract(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = append(initiative.Edges,
		domain.InitiativeEdge{
			FromTaskHandle: "task-backend", ToTaskHandle: "task-frontend",
			Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema,
		},
	)

	stale := initiative.TasksStaleAfterSupersession(domain.ArtifactAPISchema, "task-backend")
	// Exactly the consumer, and nothing else. The integration lane depends on
	// backend too, but through an integrates_after edge that consumes no
	// artifact — staling it would invalidate evidence the change cannot affect.
	if len(stale) != 1 || stale[0] != "task-frontend" {
		t.Fatalf("stale = %v", stale)
	}
}

func TestSupersessionStalesNothingWhenNoConsumerWantsThatKind(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = append(initiative.Edges,
		domain.InitiativeEdge{
			FromTaskHandle: "task-backend", ToTaskHandle: "task-frontend",
			Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema,
		},
	)
	stale := initiative.TasksStaleAfterSupersession(domain.ArtifactFixture, "task-backend")
	if len(stale) != 0 {
		t.Fatalf("unrelated artifact kind staled %v", stale)
	}
}

func TestSupersessionStalesNothingForAProducerOutsideTheInitiative(t *testing.T) {
	initiative := initiativeFixture()
	stale := initiative.TasksStaleAfterSupersession(domain.ArtifactAPISchema, "task-elsewhere")
	if len(stale) != 0 {
		t.Fatalf("foreign producer staled %v", stale)
	}
}

func TestHeadChangeStalesTheProducerAndItsArtifactConsumers(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = append(initiative.Edges,
		domain.InitiativeEdge{
			FromTaskHandle: "task-backend", ToTaskHandle: "task-frontend",
			Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema,
		},
	)

	stale := initiative.TasksStaleAfterHeadChange("task-backend")
	// The producer itself first: its own validation evidence was gathered
	// against content that no longer exists. Then whoever pinned an artifact it
	// produced, because that artifact was built from the old head.
	if len(stale) != 2 || stale[0] != "task-backend" || stale[1] != "task-frontend" {
		t.Fatalf("stale = %v", stale)
	}
}

func TestHeadChangeLeavesLanesThatConsumeNothingAlone(t *testing.T) {
	initiative := initiativeFixture()
	// task-integration depends on task-backend through integrates_after, which
	// consumes no artifact. Its evidence is about integration, not about the
	// producer's content, so a head move must not discard it.
	stale := initiative.TasksStaleAfterHeadChange("task-backend")
	if len(stale) != 1 || stale[0] != "task-backend" {
		t.Fatalf("stale = %v", stale)
	}
}

func TestHeadChangeForANonMemberStalesNothing(t *testing.T) {
	initiative := initiativeFixture()
	if stale := initiative.TasksStaleAfterHeadChange("task-elsewhere"); len(stale) != 0 {
		t.Fatalf("foreign task staled %v", stale)
	}
}

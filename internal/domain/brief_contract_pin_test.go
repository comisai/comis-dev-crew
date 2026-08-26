package domain

import (
	"strings"
	"testing"
)

func pinnedTask(t *testing.T) Task {
	t.Helper()
	task := validTask(ShapeShip, DeliveryPullRequest)
	task.ConsumedContracts = []PinnedContract{
		{
			ArtifactHandle: "artifact-api-v1",
			Kind:           ArtifactAPISchema,
			ContentHash:    strings.Repeat("a", 64),
		},
	}
	pinned, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	return pinned
}

func TestBriefCarriesTheExactContractDigestAConsumerPinned(t *testing.T) {
	brief, err := pinnedTask(t).RenderWorkerBrief()
	if err != nil {
		t.Fatalf("RenderWorkerBrief() error = %v", err)
	}
	// The digest travels IN the brief. A consumer that only received a handle
	// could be handed different bytes under the same name and never know.
	if !strings.Contains(brief.Content, "artifact-api-v1") {
		t.Fatalf("brief omits the artifact handle:\n%s", brief.Content)
	}
	if !strings.Contains(brief.Content, strings.Repeat("a", 64)) {
		t.Fatalf("brief omits the pinned digest:\n%s", brief.Content)
	}
	if !strings.Contains(brief.Content, string(ArtifactAPISchema)) {
		t.Fatalf("brief omits the artifact kind:\n%s", brief.Content)
	}
}

func TestChangingAPinnedDigestChangesTheBriefRevisionHash(t *testing.T) {
	first := pinnedTask(t)
	second := validTask(ShapeShip, DeliveryPullRequest)
	second.ConsumedContracts = []PinnedContract{
		{
			ArtifactHandle: "artifact-api-v1",
			Kind:           ArtifactAPISchema,
			ContentHash:    strings.Repeat("b", 64),
		},
	}
	repinned, err := second.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	// Same handle, different bytes. If the revision hash did not move, a
	// superseded contract could ride into a running worker unnoticed — which is
	// the exact failure immutable handoffs exist to prevent.
	if repinned.BriefRevisionHash == first.BriefRevisionHash {
		t.Fatal("a changed contract digest left the brief revision hash unchanged")
	}
}

func TestPinnedContractsAreOrderIndependent(t *testing.T) {
	build := func(order []string) string {
		task := validTask(ShapeShip, DeliveryPullRequest)
		for _, handle := range order {
			task.ConsumedContracts = append(task.ConsumedContracts, PinnedContract{
				ArtifactHandle: handle, Kind: ArtifactAPISchema,
				ContentHash: strings.Repeat("a", 64),
			})
		}
		pinned, err := task.PinBriefRevision()
		if err != nil {
			t.Fatalf("PinBriefRevision() error = %v", err)
		}
		return pinned.BriefRevisionHash
	}
	// The same pins listed in a different order are the same contract. A brief
	// whose hash depended on ordering would churn for no semantic reason.
	if build([]string{"artifact-a", "artifact-b"}) != build([]string{"artifact-b", "artifact-a"}) {
		t.Fatal("pin ordering changed the brief revision hash")
	}
}

func TestABriefRefusesAPinWithoutADigest(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	task.ConsumedContracts = []PinnedContract{
		{ArtifactHandle: "artifact-api-v1", Kind: ArtifactAPISchema},
	}
	if _, err := task.PinBriefRevision(); err == nil {
		t.Fatal("a pin without a digest was accepted")
	}
}

func TestABriefRefusesDuplicatePinsOfOneArtifact(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	pin := PinnedContract{
		ArtifactHandle: "artifact-api-v1", Kind: ArtifactAPISchema,
		ContentHash: strings.Repeat("a", 64),
	}
	task.ConsumedContracts = []PinnedContract{pin, pin}
	if _, err := task.PinBriefRevision(); err == nil {
		t.Fatal("duplicate pins accepted")
	}
}

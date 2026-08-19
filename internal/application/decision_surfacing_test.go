package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func surfacingClock(at time.Time) Clock { return func() time.Time { return at } }

type stubSurfacingStore struct {
	open      []OpenDecision
	openErr   error
	recorded  []string
	recordErr error
}

func (stub *stubSurfacingStore) OpenDecisionsAwaitingHuman(context.Context) ([]OpenDecision, error) {
	return stub.open, stub.openErr
}

func (stub *stubSurfacingStore) RecordDecisionSurfaced(
	_ context.Context,
	mutation DecisionSurfacedMutation,
) error {
	stub.recorded = append(stub.recorded, mutation.TaskHandle+":"+mutation.ExternalKey)
	return stub.recordErr
}

// The first surfacing is immediate: an open decision wakes the liaison once as
// soon as it exists. Nothing has been said yet, so there is nothing to wait for.
func TestDecisionSurfacingPolicy_MakesAnUnsurfacedDecisionDueImmediately(t *testing.T) {
	policy := DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour}
	now := time.Unix(1_800_000_000, 0).UTC()

	if !policy.Due(OpenDecision{}, now) {
		t.Error("a decision nobody has surfaced must be due at once")
	}
}

// After a surfacing the cadence is bounded: the same question is not repeated
// until the interval has passed. Bounded means the rate, not the end — an open
// decision keeps coming back, because the alternative is a question that was
// asked once and then silently forgotten.
func TestDecisionSurfacingPolicy_BoundsTheRateWithoutEverGivingUp(t *testing.T) {
	policy := DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour}
	surfaced := time.Unix(1_800_000_000, 0).UTC()

	justSurfaced := OpenDecision{SurfaceCount: 1, LastSurfacedAt: surfaced}
	if policy.Due(justSurfaced, surfaced.Add(29*time.Minute)) {
		t.Error("a decision surfaced 29 minutes ago must not repeat inside its interval")
	}
	if !policy.Due(justSurfaced, surfaced.Add(30*time.Minute)) {
		t.Error("a decision must repeat once its interval has passed")
	}

	// However long it has been open, it is still due again eventually.
	weathered := OpenDecision{SurfaceCount: 500, LastSurfacedAt: surfaced}
	if !policy.Due(weathered, surfaced.Add(4*time.Hour)) {
		t.Error("an old decision must still re-surface; bounded is the rate, not the end")
	}
}

// The interval grows so a question nobody is answering stops competing with
// fresh work, and it stops growing so it never effectively disappears.
func TestDecisionSurfacingPolicy_BacksOffToACapAndNoFurther(t *testing.T) {
	policy := DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour}

	for count, want := range map[int]time.Duration{
		0: 0,
		1: 30 * time.Minute,
		2: time.Hour,
		3: 2 * time.Hour,
		4: 4 * time.Hour,
		5: 4 * time.Hour,
		9: 4 * time.Hour,
	} {
		if interval := policy.Interval(count); interval != want {
			t.Errorf("interval after %d surfacings = %v, want %v", count, interval, want)
		}
	}
}

func TestDecisionSurfacingPolicy_RejectsAnIncoherentCadence(t *testing.T) {
	for label, policy := range map[string]DecisionSurfacingPolicy{
		"no initial":            {Maximum: time.Hour},
		"no maximum":            {Initial: time.Minute},
		"negative initial":      {Initial: -time.Minute, Maximum: time.Hour},
		"maximum below initial": {Initial: 2 * time.Hour, Maximum: time.Hour},
	} {
		if err := policy.Validate(); err == nil {
			t.Errorf("Validate(%s) error = nil", label)
		}
	}
	if err := (DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour}).Validate(); err != nil {
		t.Errorf("Validate(coherent) error = %v", err)
	}
}

// Only decisions whose interval has elapsed are returned, so a caller cannot
// re-ask everything at once simply by running more often.
func TestDueDecisions_ReturnsOnlyThoseWhoseIntervalElapsed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &stubSurfacingStore{open: []OpenDecision{
		{TaskHandle: "task-0001", ExternalKey: "never-asked"},
		{TaskHandle: "task-0002", ExternalKey: "asked-recently", SurfaceCount: 1, LastSurfacedAt: now.Add(-time.Minute)},
		{TaskHandle: "task-0003", ExternalKey: "asked-long-ago", SurfaceCount: 1, LastSurfacedAt: now.Add(-time.Hour)},
	}}
	surfacer := newSurfacer(t, store, now)

	due, err := surfacer.DueDecisions(context.Background())
	if err != nil {
		t.Fatalf("DueDecisions() error = %v", err)
	}
	if len(due) != 2 || due[0].ExternalKey != "never-asked" || due[1].ExternalKey != "asked-long-ago" {
		t.Fatalf("due decisions = %+v", due)
	}
}

// The surfacing is recorded before it can count as done, so a restart replays
// at worst one repeat rather than resetting every decision to due-now and
// waking the liaison with all of them at once.
func TestRecordSurfaced_PersistsTheAttemptAndSurfacesItsFailure(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	store := &stubSurfacingStore{}
	surfacer := newSurfacer(t, store, now)

	if err := surfacer.RecordSurfaced(context.Background(), OpenDecision{
		TaskHandle: "task-0001", ExternalKey: "schema-choice",
	}); err != nil {
		t.Fatalf("RecordSurfaced() error = %v", err)
	}
	if len(store.recorded) != 1 || store.recorded[0] != "task-0001:schema-choice" {
		t.Fatalf("recorded = %v", store.recorded)
	}

	failing := newSurfacer(t, &stubSurfacingStore{recordErr: errors.New("store unavailable")}, now)
	if err := failing.RecordSurfaced(context.Background(), OpenDecision{
		TaskHandle: "task-0001", ExternalKey: "schema-choice",
	}); err == nil {
		t.Fatal("RecordSurfaced(failing store) error = nil")
	}
}

func TestRecordSurfaced_RefusesAnUnidentifiedDecision(t *testing.T) {
	surfacer := newSurfacer(t, &stubSurfacingStore{}, time.Unix(1_800_000_000, 0).UTC())
	for label, decision := range map[string]OpenDecision{
		"no task": {ExternalKey: "schema-choice"},
		"no key":  {TaskHandle: "task-0001"},
		"forged":  {TaskHandle: "../../etc", ExternalKey: "schema-choice"},
	} {
		if err := surfacer.RecordSurfaced(context.Background(), decision); err == nil {
			t.Errorf("RecordSurfaced(%s) error = nil", label)
		}
	}
}

func TestNewDecisionSurfacer_RequiresEverySeam(t *testing.T) {
	policy := DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour}
	for label, config := range map[string]DecisionSurfacingConfig{
		"nothing":     {},
		"no store":    {Policy: policy, Clock: time.Now},
		"no clock":    {Store: &stubSurfacingStore{}, Policy: policy},
		"bad cadence": {Store: &stubSurfacingStore{}, Clock: time.Now},
	} {
		if _, err := NewDecisionSurfacer(config); err == nil {
			t.Errorf("NewDecisionSurfacer(%s) error = nil", label)
		}
	}
}

func TestDueDecisions_SurfacesAStoreFailure(t *testing.T) {
	failure := errors.New("store unavailable")
	surfacer := newSurfacer(t, &stubSurfacingStore{openErr: failure}, time.Unix(1_800_000_000, 0).UTC())
	if _, err := surfacer.DueDecisions(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("DueDecisions() error = %v, want the store failure", err)
	}
}

func newSurfacer(t *testing.T, store DecisionSurfacingStore, now time.Time) *DecisionSurfacer {
	t.Helper()
	surfacer, err := NewDecisionSurfacer(DecisionSurfacingConfig{
		Store:  store,
		Policy: DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour},
		Clock:  surfacingClock(now),
	})
	if err != nil {
		t.Fatalf("NewDecisionSurfacer() error = %v", err)
	}
	return surfacer
}

package application

import (
	"context"
	"errors"
	"testing"
)

type stubPrimarySynchronizer struct {
	calls  []string
	report PrimarySyncReport
	err    error
}

func (stub *stubPrimarySynchronizer) SynchronizePrimary(
	_ context.Context,
	command PrimarySyncCommand,
) (PrimarySyncReport, error) {
	stub.calls = append(stub.calls, command.OperationID+":"+command.RepositoryID)
	return stub.report, stub.err
}

type stubStateVersions struct {
	version int64
	err     error
}

func (stub stubStateVersions) CurrentStateVersion(context.Context) (int64, error) {
	return stub.version, stub.err
}

func newPrimaryCheckouts(t *testing.T, stub *stubPrimarySynchronizer) *PrimaryCheckouts {
	t.Helper()
	checkouts, err := NewPrimaryCheckouts(PrimaryCheckoutConfig{
		Synchronizer: stub, StateVersions: stubStateVersions{version: 12},
	})
	if err != nil {
		t.Fatalf("NewPrimaryCheckouts() error = %v", err)
	}
	return checkouts
}

// The synchronizer is reached only with an exact operation and repository
// identity. Anything else is refused before the adapter runs, so a malformed
// request can never become a Git command against an unintended tree.
func TestSyncPrimary_RefusesMalformedIdentityBeforeReachingTheAdapter(t *testing.T) {
	stub := &stubPrimarySynchronizer{report: PrimarySyncReport{Outcome: PrimarySyncAlreadyCurrent}}
	checkouts := newPrimaryCheckouts(t, stub)

	for label, command := range map[string]PrimarySyncCommand{
		"no operation":      {RepositoryID: "product-api"},
		"no repository":     {OperationID: "operation-sync-0001"},
		"forged repository": {OperationID: "operation-sync-0001", RepositoryID: "../../etc"},
	} {
		if _, err := checkouts.SyncPrimary(context.Background(), command); err == nil {
			t.Errorf("SyncPrimary(%s) error = nil", label)
		}
	}
	if len(stub.calls) != 0 {
		t.Fatalf("a malformed request reached the adapter: %v", stub.calls)
	}
}

// A refusal is a completed operation with a named posture, not an error. The
// checkout was inspected and found unfit to advance, which is an answer the
// operator can act on.
func TestSyncPrimary_ReportsARefusalAsACompletedOperation(t *testing.T) {
	stub := &stubPrimarySynchronizer{report: PrimarySyncReport{
		RepositoryID: "product-api", Branch: "main", Outcome: PrimarySyncRefused,
		Refusal: PrimarySyncRefusalDirty, PreviousHead: "a", Head: "a",
	}}
	checkouts := newPrimaryCheckouts(t, stub)

	report, err := checkouts.SyncPrimary(context.Background(), PrimarySyncCommand{
		OperationID: "operation-sync-0001", RepositoryID: "product-api",
	})
	if err != nil {
		t.Fatalf("SyncPrimary() error = %v", err)
	}
	if report.Outcome != PrimarySyncRefused || report.Refusal != PrimarySyncRefusalDirty {
		t.Fatalf("report = %#v, want a named refusal", report)
	}
	if len(stub.calls) != 1 || stub.calls[0] != "operation-sync-0001:product-api" {
		t.Fatalf("adapter calls = %v", stub.calls)
	}
}

// An adapter failure is surfaced, never softened into a refusal: an operator
// told their checkout was dirty when Git was actually unreachable would repair
// the wrong thing.
func TestSyncPrimary_SurfacesAnAdapterFailureRatherThanInventingAPosture(t *testing.T) {
	failure := errors.New("git unavailable")
	stub := &stubPrimarySynchronizer{err: failure}
	checkouts := newPrimaryCheckouts(t, stub)

	report, err := checkouts.SyncPrimary(context.Background(), PrimarySyncCommand{
		OperationID: "operation-sync-0001", RepositoryID: "product-api",
	})
	if !errors.Is(err, failure) {
		t.Fatalf("SyncPrimary() error = %v, want the adapter failure", err)
	}
	if report.Outcome != "" {
		t.Fatalf("a failed sync reported outcome %q", report.Outcome)
	}
}

func TestNewPrimaryCheckouts_RequiresEverySeam(t *testing.T) {
	for label, config := range map[string]PrimaryCheckoutConfig{
		"nothing":           {},
		"no synchronizer":   {StateVersions: stubStateVersions{}},
		"no state versions": {Synchronizer: &stubPrimarySynchronizer{}},
	} {
		if _, err := NewPrimaryCheckouts(config); err == nil {
			t.Errorf("NewPrimaryCheckouts(%s) error = nil", label)
		}
	}
}

// The report carries the service's own state version as the connection's
// read-after-write token. Without it the projection cannot travel over the
// local API at all, so a lookup failure is surfaced rather than defaulted.
func TestSyncPrimary_CarriesTheServiceStateVersionAndSurfacesItsFailure(t *testing.T) {
	stub := &stubPrimarySynchronizer{report: PrimarySyncReport{Outcome: PrimarySyncAlreadyCurrent}}
	checkouts := newPrimaryCheckouts(t, stub)
	report, err := checkouts.SyncPrimary(context.Background(), PrimarySyncCommand{
		OperationID: "operation-sync-0001", RepositoryID: "product-api",
	})
	if err != nil || report.StateVersion != 12 || report.SchemaVersion != 1 {
		t.Fatalf("report = %#v, %v", report, err)
	}

	failing, err := NewPrimaryCheckouts(PrimaryCheckoutConfig{
		Synchronizer:  &stubPrimarySynchronizer{report: PrimarySyncReport{Outcome: PrimarySyncAlreadyCurrent}},
		StateVersions: stubStateVersions{err: errors.New("store unavailable")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.SyncPrimary(context.Background(), PrimarySyncCommand{
		OperationID: "operation-sync-0001", RepositoryID: "product-api",
	}); err == nil {
		t.Fatal("SyncPrimary(failing state version) error = nil")
	}
}

func TestSyncPrimary_RefusesACanceledCaller(t *testing.T) {
	checkouts := newPrimaryCheckouts(t, &stubPrimarySynchronizer{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := checkouts.SyncPrimary(canceled, PrimarySyncCommand{
		OperationID: "operation-sync-0001", RepositoryID: "product-api",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncPrimary(canceled) error = %v, want context.Canceled", err)
	}
}

package localapi

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type stubPrimaryCheckouts struct {
	calls  []string
	report application.PrimarySyncReport
	err    error
}

func (stub *stubPrimaryCheckouts) SyncPrimary(
	_ context.Context,
	command application.PrimarySyncCommand,
) (application.PrimarySyncReport, error) {
	stub.calls = append(stub.calls, command.OperationID+":"+command.RepositoryID)
	return stub.report, stub.err
}

func newSyncClient(t *testing.T, checkouts PrimaryCheckoutSync) *Client {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, PrimaryCheckouts: checkouts, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := filepath.Join(canonicalTempDir(t), "runtime", "devcrew.sock")
	server, err := Listen(socketPath, CallerOperatorCLI, handler)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server.Close() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Server.Serve() error = %v", err)
		}
	})
	client, err := NewClient(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

// The whole report survives the round trip, refusal included. The posture is
// the part an operator acts on, so a projection that carried only the outcome
// would tell them their checkout did not advance without saying why.
func TestLocalAPI_SyncPrimaryCarriesTheRefusalAcrossTheRoundTrip(t *testing.T) {
	checkouts := &stubPrimaryCheckouts{report: application.PrimarySyncReport{
		SchemaVersion: 1, StateVersion: 31, RepositoryID: "product-api", Branch: "main",
		PreviousHead: "aaa", Head: "aaa",
		Outcome: application.PrimarySyncRefused,
		Refusal: application.PrimarySyncRefusalDivergent,
	}}
	client := newSyncClient(t, checkouts)

	report, err := client.SyncPrimary(context.Background(), "operation-sync-0001", SyncPrimaryInput{
		RepositoryID: "product-api",
	})
	if err != nil {
		t.Fatalf("SyncPrimary() error = %v", err)
	}
	if report.Outcome != application.PrimarySyncRefused || report.Refusal != application.PrimarySyncRefusalDivergent {
		t.Fatalf("report = %#v, want the refusal to survive", report)
	}
	if report.Branch != "main" || report.RepositoryID != "product-api" {
		t.Errorf("report identity = %#v", report)
	}
	if len(checkouts.calls) != 1 || checkouts.calls[0] != "operation-sync-0001:product-api" {
		t.Fatalf("canonical calls = %v", checkouts.calls)
	}
}

func TestLocalAPI_SyncPrimaryCarriesAnUpdateAcrossTheRoundTrip(t *testing.T) {
	checkouts := &stubPrimaryCheckouts{report: application.PrimarySyncReport{
		SchemaVersion: 1, StateVersion: 31, RepositoryID: "product-api", Branch: "main",
		PreviousHead: "aaa", Head: "bbb", Outcome: application.PrimarySyncUpdated,
	}}
	client := newSyncClient(t, checkouts)

	report, err := client.SyncPrimary(context.Background(), "operation-sync-0001", SyncPrimaryInput{
		RepositoryID: "product-api",
	})
	if err != nil {
		t.Fatalf("SyncPrimary() error = %v", err)
	}
	if report.Outcome != application.PrimarySyncUpdated || report.Head != "bbb" || report.PreviousHead != "aaa" {
		t.Fatalf("report = %#v, want the update to survive", report)
	}
}

// A deployment that configured no repositories cannot synchronize one. That is
// reported as an unavailable service surface, not as a checkout posture: there
// is no checkout to describe.
func TestLocalAPI_SyncPrimaryReportsAnAbsentSurfaceRatherThanARefusal(t *testing.T) {
	client := newSyncClient(t, nil)

	if _, err := client.SyncPrimary(context.Background(), "operation-sync-0001", SyncPrimaryInput{
		RepositoryID: "product-api",
	}); err == nil {
		t.Fatal("SyncPrimary(absent surface) error = nil")
	}
}

// An application failure travels as a failure. Softening it into a refusal
// would send an operator to tidy a checkout that was never the problem.
func TestLocalAPI_SyncPrimarySurfacesAnApplicationFailure(t *testing.T) {
	checkouts := &stubPrimaryCheckouts{err: &domain.Failure{
		Code: domain.ErrorUnavailable, Retryable: true, Message: "git unavailable",
	}}
	client := newSyncClient(t, checkouts)

	if _, err := client.SyncPrimary(context.Background(), "operation-sync-0001", SyncPrimaryInput{
		RepositoryID: "product-api",
	}); err == nil {
		t.Fatal("SyncPrimary(failing application) error = nil")
	}
}

// An incomplete projection is refused rather than published. A report with no
// outcome would reach an operator surface as a blank posture.
func TestLocalAPI_SyncPrimaryRefusesAnIncompleteProjection(t *testing.T) {
	client := newSyncClient(t, &stubPrimaryCheckouts{report: application.PrimarySyncReport{
		SchemaVersion: 1, StateVersion: 31, RepositoryID: "product-api",
	}})

	if _, err := client.SyncPrimary(context.Background(), "operation-sync-0001", SyncPrimaryInput{
		RepositoryID: "product-api",
	}); err == nil {
		t.Fatal("SyncPrimary(incomplete projection) error = nil")
	}
}

// The method is a mutation. Its classification is what adapters read to decide
// approval and side-effect metadata, so it is pinned here rather than inferred.
func TestLocalAPI_SyncPrimaryIsAClassifiedMutation(t *testing.T) {
	if !MethodSyncPrimary.valid() {
		t.Fatal("MethodSyncPrimary is not a valid method")
	}
	if MethodSyncPrimary.SideEffect() != SideEffectMutate {
		t.Fatalf("side effect = %q, want mutate", MethodSyncPrimary.SideEffect())
	}
}

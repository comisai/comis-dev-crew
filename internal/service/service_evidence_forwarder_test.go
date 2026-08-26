package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRun_SupervisesDurableCandidateEvidenceForwarding(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	store, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := serviceTask()
	task.State = domain.TaskWorking
	task.ManagedRunID = "managed-run-evidence"
	task.WorkspaceLeaseID = "workspace-lease-evidence"
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	producedAt := serviceForwarderClock().Add(-time.Hour)
	reportAt := producedAt.Add(-time.Minute)
	sink, err := application.NewReportSink(application.ReportSinkConfig{
		Store: store, Clock: func() time.Time { return reportAt },
	})
	if err != nil {
		t.Fatalf("NewReportSink() error = %v", err)
	}
	if _, err := sink.AcceptReport(context.Background(), domain.AuthenticatedReport{
		TaskHandle: task.Handle,
		Report: domain.WorkerReport{
			SchemaVersion: 1, LocalReportID: "service-report-candidate-evidence",
			BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
			Kind: domain.ReportCandidateComplete, Summary: "Deterministic candidate is ready.",
		},
	}); err != nil {
		t.Fatalf("AcceptReport(candidate) error = %v", err)
	}
	head := strings.Repeat("b", 40)
	sealed, err := domain.SealDeliveryEvidence(domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: head, WorktreeCleanliness: domain.WorktreeClean,
		ValidationReceipts: []domain.ValidationEvidenceReceipt{{
			CheckID: "unit", ProgramID: "go-test", HeadRevision: head, Conclusion: domain.CheckPassed,
			Required: true, OutputHash: strings.Repeat("d", 64),
			StartedAt: producedAt.Add(-time.Minute), CompletedAt: producedAt,
		}},
		ForgeEvidence: &domain.ForgeEvidence{
			Repository: task.RepositoryID, PullRequestID: "pull-request-evidence", Branch: "devcrew/task-evidence",
			HeadRevision:     head,
			CheckConclusions: []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
		},
		ProducedAt: producedAt, ExpiresAt: producedAt.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("SealDeliveryEvidence() error = %v", err)
	}
	publications, err := candidateEvidencePublications(
		task, sealed, candidateDeliveryMaterial{referenceURL: "https://example.com/pull/17"},
	)
	if err != nil {
		t.Fatalf("candidateEvidencePublications() error = %v", err)
	}
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, producedAt, publications,
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	control := &serviceComisControl{
		reports:  make(chan comiswire.ReportRequestParams, 1),
		evidence: make(chan comiswire.PutEvidenceRequestParams, 2),
	}
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: filepath.Join(root, "run", "devcrew.sock"),
			ComisControl: control, Clock: serviceForwarderClock, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	requests := make([]comiswire.PutEvidenceRequestParams, 2)
	for index := range requests {
		select {
		case requests[index] = <-control.evidence:
		case err := <-done:
			t.Fatalf("Run() before evidence %d error = %v", index+1, err)
		case <-time.After(time.Second):
			t.Fatalf("evidence %d was not forwarded", index+1)
		}
	}
	if requests[0].EvidenceRef == requests[1].EvidenceRef ||
		requests[0].SubjectDigest != requests[1].SubjectDigest {
		t.Fatalf("evidence requests = %#v / %#v", requests[0], requests[1])
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() cancellation error = %v", err)
	}
}

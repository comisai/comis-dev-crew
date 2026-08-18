package workers_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// Resume relaunches a worker onto the tree it left. The head it left is pinned,
// because a worker resumed onto a tree that moved would continue against work it
// never saw and report against a base that no longer exists.
func TestAdapters_ResumeDescriptorPinsTheHeadTheWorkerLeft(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			launch := resumeLaunchRequest(t, name)
			head := strings.Repeat("b", 40)

			descriptor, err := adapter.BuildResumeDescriptor(context.Background(), application.WorkerResumeRequest{
				Launch: launch, ResumeFromHead: head,
			})
			if err != nil {
				t.Fatalf("BuildResumeDescriptor() error = %v", err)
			}
			if descriptor.WorkingDirectory != launch.WorkingDirectory {
				t.Errorf("resume working directory = %q, want the tree the worker left", descriptor.WorkingDirectory)
			}
			// The bootstrap must tell the worker it is continuing. A worker
			// handed the launch bootstrap would start over on top of its own
			// unfinished work.
			joined := strings.Join(descriptor.Arguments, "\x00")
			if !strings.Contains(joined, head) {
				t.Errorf("resume bootstrap does not pin the head %q", head)
			}
			if !strings.Contains(strings.ToLower(joined), "resum") && !strings.Contains(strings.ToLower(joined), "continu") {
				t.Error("resume bootstrap does not tell the worker it is continuing existing work")
			}
			// Task authority still never travels in argv.
			for _, secret := range []string{launch.ManagedRunID, launch.WorkspaceLeaseID, launch.TaskHandle} {
				if strings.Contains(joined, secret) {
					t.Errorf("resume argv leaked authority %q", secret)
				}
			}

			for label, broken := range map[string]string{
				"absent": "", "short": strings.Repeat("b", 39),
				"uppercase": strings.Repeat("B", 40), "not hex": strings.Repeat("z", 40),
			} {
				if _, err := adapter.BuildResumeDescriptor(context.Background(), application.WorkerResumeRequest{
					Launch: launch, ResumeFromHead: broken,
				}); err == nil {
					t.Errorf("BuildResumeDescriptor(%s head) error = nil", label)
				}
			}
		})
	}
}

// The lifecycle integration is how the service learns a worker turn settled.
// Neither shipped family was built with a verified settle signal, so both must
// say so and neither may claim unattended readiness it cannot prove.
func TestAdapters_LifecycleIntegrationRefusesToClaimAnUnverifiedSettleSignal(t *testing.T) {
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			integration, err := adapter.InstallLifecycleIntegration(context.Background(), application.LifecycleIntegrationRequest{
				ProfileID: interventionProfileID(name), TaskHandle: "task-0001",
			})
			if err != nil {
				t.Fatalf("InstallLifecycleIntegration() error = %v", err)
			}
			if integration.SettleSignalVerified {
				t.Error("integration claims a settle signal that was never verified")
			}
			if integration.Reason != application.HarnessReasonLifecycleSignalUnknown {
				t.Errorf("integration reason = %q, want the unknown-signal reason", integration.Reason)
			}
			// Every artifact is a relative path the service materializes under
			// the task's lease root. An absolute or escaping path would let the
			// adapter choose a destination outside the containment the lease owns.
			for _, artifact := range integration.Artifacts {
				if strings.HasPrefix(artifact.RelativePath, "/") || strings.Contains(artifact.RelativePath, "..") {
					t.Errorf("artifact path %q escapes the lease root", artifact.RelativePath)
				}
			}
			if _, err := adapter.InstallLifecycleIntegration(context.Background(), application.LifecycleIntegrationRequest{
				ProfileID: "someone-elses-profile", TaskHandle: "task-0001",
			}); err == nil {
				t.Error("InstallLifecycleIntegration(foreign profile) error = nil")
			}
		})
	}
}

// Usage is reported only when a producer actually emitted it. Absence is
// unknown and never zero: a zero cost is a claim, and an unattributed one would
// understate a ceiling the operator set.
func TestAdapters_CollectUsageReportsUnknownRatherThanZero(t *testing.T) {
	now := time.Now()
	for name, adapter := range interventionAdapters(t) {
		t.Run(name, func(t *testing.T) {
			for label, observation := range map[string]application.UsageObservation{
				"no event":  {ProfileID: interventionProfileID(name), ObservedAt: now, Now: now, FreshnessTTL: time.Minute},
				"malformed": {ProfileID: interventionProfileID(name), EventJSON: []byte("{"), ObservedAt: now, Now: now, FreshnessTTL: time.Minute},
				"no usage":  {ProfileID: interventionProfileID(name), EventJSON: []byte(`{"type":"result"}`), ObservedAt: now, Now: now, FreshnessTTL: time.Minute},
				"stale": {
					ProfileID:  interventionProfileID(name),
					EventJSON:  []byte(`{"type":"result","usage":{"input_tokens":10,"output_tokens":2}}`),
					ObservedAt: now.Add(-time.Hour), Now: now, FreshnessTTL: time.Minute,
				},
			} {
				usage, err := adapter.CollectUsage(context.Background(), observation)
				if err != nil {
					t.Fatalf("CollectUsage(%s) error = %v", label, err)
				}
				if usage.Known {
					t.Errorf("CollectUsage(%s) claimed known usage %#v", label, usage)
				}
				if usage.InputTokens != 0 || usage.OutputTokens != 0 {
					t.Errorf("CollectUsage(%s) reported counts without a producer: %#v", label, usage)
				}
				if usage.Reason == "" {
					t.Errorf("CollectUsage(%s) gives no reason for unknown usage", label)
				}
			}

			fresh := application.UsageObservation{
				ProfileID:  interventionProfileID(name),
				EventJSON:  []byte(`{"type":"result","usage":{"input_tokens":120,"output_tokens":34}}`),
				ObservedAt: now, Now: now, FreshnessTTL: time.Minute,
			}
			usage, err := adapter.CollectUsage(context.Background(), fresh)
			if err != nil {
				t.Fatal(err)
			}
			if !usage.Known || usage.InputTokens != 120 || usage.OutputTokens != 34 {
				t.Fatalf("CollectUsage(fresh) = %#v, want the emitted counts", usage)
			}
		})
	}
}

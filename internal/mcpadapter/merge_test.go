package mcpadapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFacade_MergeTaskIsAnExplicitDestructiveOpenWorldTool(t *testing.T) {
	facade, err := New(Config{
		Client: &fakeClient{}, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := connectFacade(t, facade).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tools.Tools {
		if listed.Name != "merge_task" {
			continue
		}
		if listed.Annotations == nil || listed.Annotations.ReadOnlyHint ||
			!listed.Annotations.IdempotentHint || listed.Annotations.DestructiveHint == nil ||
			!*listed.Annotations.DestructiveHint || listed.Annotations.OpenWorldHint == nil ||
			!*listed.Annotations.OpenWorldHint {
			t.Fatalf("merge_task annotations = %#v", listed.Annotations)
		}
		return
	}
	t.Fatal("merge_task tool is absent")
}

func TestFacade_MergeTaskConsumesOnlyPrivateApprovalMetadata(t *testing.T) {
	client := &fakeClient{mergeResult: completedMergeResult()}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
		Meta: mergeCallMeta(), Name: ToolMergeTask, Arguments: TaskInput{TaskHandle: "task-0001"},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool(merge_task) = %#v, %v", result, err)
	}
	wantCall := "merge:merge-0001:task-0001:" + mergeApprovalID + ":merge-0001"
	if got := strings.Join(client.calls, ","); got != wantCall {
		t.Fatalf("merge calls = %q, want %q", got, wantCall)
	}
	visible, ok := result.StructuredContent.(map[string]any)
	if !ok || visible["state"] != string(application.TaskMergeCompleted) ||
		visible["sideEffect"] != string(localapi.SideEffectMutate) || visible["completedAtMs"] != float64(completedMergeAt.UnixMilli()) {
		t.Fatalf("merge output = %#v", result.StructuredContent)
	}
}

func TestFacade_MergeTaskRefusesAbsentOrMalformedPrivateAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "approval absent", mutate: func(value map[string]any) { value["managedRunId"] = "managed-run-0001" }},
		{name: "managed run absent", mutate: func(value map[string]any) { value["approvalRequestId"] = mergeApprovalID }},
		{name: "approval malformed", mutate: func(value map[string]any) {
			value["approvalRequestId"], value["managedRunId"] = "not-an-approval", "managed-run-0001"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{mergeResult: completedMergeResult()}
			facade, err := New(Config{
				Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
				NewOperationID: func() (string, error) { return "reconcile-0001", nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			meta := callMeta("merge-0001", "service-instance-0001")
			test.mutate(meta[CallContextMetaKey].(map[string]any))
			result, callErr := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
				Meta: meta, Name: ToolMergeTask, Arguments: TaskInput{TaskHandle: "task-0001"},
			})
			if callErr != nil || result == nil || !result.IsError || len(client.calls) != 0 {
				t.Fatalf("CallTool(merge_task) = %#v, %v, calls=%v", result, callErr, client.calls)
			}
		})
	}
}

func TestFacade_MergeTaskRejectsForgeAndApprovalArguments(t *testing.T) {
	client := &fakeClient{mergeResult: completedMergeResult()}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
		Meta: mergeCallMeta(), Name: ToolMergeTask, Arguments: map[string]any{
			"taskHandle": "task-0001", "approvalRequestId": mergeApprovalID,
			"repositoryId": "other-repository", "pullRequestId": "github-pr-999",
			"headRevision": strings.Repeat("c", 40), "method": "rebase",
		},
	})
	if err != nil || result == nil || !result.IsError || len(client.calls) != 0 {
		t.Fatalf("CallTool(merge_task with forged authority) = %#v, %v, calls=%v", result, err, client.calls)
	}
}

func TestFacade_MergeTaskRejectsAnyNonExactCompletion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*application.MergeTaskResult)
	}{
		{name: "pending", mutate: func(result *application.MergeTaskResult) { result.State = application.TaskMergeAwaitingApproval }},
		{name: "operation differs", mutate: func(result *application.MergeTaskResult) { result.OperationID = "other-0001" }},
		{name: "approval differs", mutate: func(result *application.MergeTaskResult) {
			result.ApprovalRequestID = "20000000-0000-4000-8000-000000000002"
		}},
		{name: "head invalid", mutate: func(result *application.MergeTaskResult) { result.HeadRevision = "not-a-head" }},
		{name: "principal absent", mutate: func(result *application.MergeTaskResult) { result.ResolvingPrincipalID = "" }},
		{name: "method invalid", mutate: func(result *application.MergeTaskResult) { result.Method = "fast-forward" }},
		{name: "completion local", mutate: func(result *application.MergeTaskResult) { result.CompletedAt = result.CompletedAt.Local() }},
		{name: "version absent", mutate: func(result *application.MergeTaskResult) { result.StateVersion = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := completedMergeResult()
			test.mutate(&invalid)
			client := &fakeClient{mergeResult: invalid}
			facade, err := New(Config{
				Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
				NewOperationID: func() (string, error) { return "reconcile-0001", nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			result, callErr := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
				Meta: mergeCallMeta(), Name: ToolMergeTask, Arguments: TaskInput{TaskHandle: "task-0001"},
			})
			if callErr != nil || result == nil || !result.IsError {
				t.Fatalf("CallTool(merge_task) = %#v, %v, want safe error", result, callErr)
			}
		})
	}
}

func TestFacade_UncertainMergeReplaysTheSameDurableTransaction(t *testing.T) {
	unavailable, err := domain.NewFailure(domain.ErrorUnavailable, true, "send uncertain", "reconcile merge", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{mergeResult: completedMergeResult(), mergeErrors: []error{unavailable, nil}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "unused-reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mergeCallMeta()}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, result, callErr := facade.mergeTask(canceled, request, TaskInput{TaskHandle: "task-0001"})
	if callErr != nil || result.State != application.TaskMergeCompleted {
		t.Fatalf("mergeTask(uncertain) = %#v, %v", result, callErr)
	}
	wantCall := "merge:merge-0001:task-0001:" + mergeApprovalID + ":merge-0001"
	if got := strings.Join(client.calls, ","); got != wantCall+","+wantCall {
		t.Fatalf("merge calls = %q, want exact replay", got)
	}
}

const mergeApprovalID = "10000000-0000-4000-8000-000000000001"

var completedMergeAt = time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)

func mergeCallMeta() mcp.Meta {
	meta := callMeta("merge-0001", "service-instance-0001")
	value := meta[CallContextMetaKey].(map[string]any)
	value["approvalRequestId"] = mergeApprovalID
	value["managedRunId"] = "managed-run-0001"
	return meta
}

func completedMergeResult() application.MergeTaskResult {
	return application.MergeTaskResult{
		OperationID: "merge-0001", TaskHandle: "task-0001", State: application.TaskMergeCompleted,
		RepositoryID: "product-api", PullRequestID: "github-pr-42", HeadRevision: strings.Repeat("a", 40),
		ApprovalRequestID: mergeApprovalID, ResolvingPrincipalID: "operator-0001",
		MergeCommitRevision: strings.Repeat("b", 40), Method: application.PullRequestMergeSquash,
		CompletedAt: completedMergeAt, StateVersion: 27,
	}
}

func (client *fakeClient) MergeTask(
	_ context.Context,
	operationID string,
	input localapi.MergeTaskInput,
) (application.MergeTaskResult, error) {
	client.calls = append(client.calls, "merge:"+operationID+":"+input.TaskHandle+":"+input.ApprovalRequestID+":"+input.MCPOperationID)
	if len(client.mergeErrors) == 0 {
		return client.mergeResult, nil
	}
	failure := client.mergeErrors[0]
	client.mergeErrors = client.mergeErrors[1:]
	if failure != nil {
		return application.MergeTaskResult{}, failure
	}
	return client.mergeResult, nil
}

func (*backlogMCPClient) MergeTask(context.Context, string, localapi.MergeTaskInput) (application.MergeTaskResult, error) {
	return application.MergeTaskResult{}, errors.New("unexpected merge call")
}

func (*initiativeMCPClient) MergeTask(context.Context, string, localapi.MergeTaskInput) (application.MergeTaskResult, error) {
	return application.MergeTaskResult{}, errors.New("unexpected merge call")
}

func (*integrationMCPClient) MergeTask(context.Context, string, localapi.MergeTaskInput) (application.MergeTaskResult, error) {
	return application.MergeTaskResult{}, errors.New("unexpected merge call")
}

package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The catalog answers "what can run here" — identity and posture. It must never
// answer "how does it run". The executable, the argument vector, and the
// environment allowlist are launch authority: they belong to the adapter that
// builds the descriptor under an operator-reviewed entry, and a model that could
// read them could propose a launch the review never approved.
func TestFacade_WhenTheCatalogIsRead_ItCarriesNoLaunchAuthority(t *testing.T) {
	client := &fakeClient{profiles: application.WorkerProfileList{
		SchemaVersion: 1, StateVersion: 7,
		Profiles: []application.WorkerProfileSummary{{
			ProfileID: "profile_ship", Harness: "claude-code",
			AllowedShapes: []domain.TaskShape{domain.ShapeShip},
			Availability:  "available", Unattended: true, ConcurrencyLimit: 2,
		}},
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      callMeta("read-0001", "service-instance-0001"),
		Name:      ToolWorkerProfiles,
		Arguments: EmptyInput{},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(%s) = %#v, %v", ToolWorkerProfiles, result, callErr)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	rendered := strings.ToLower(string(encoded))
	for _, forbidden := range []string{
		"executable", "argv", "\"args\"", "env", "workspaceroot", "credential", "secret", "token",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("catalog leaked launch authority %q: %s", forbidden, rendered)
		}
	}
	for _, required := range []string{"profile_ship", "claude-code", "available", "unattended"} {
		if !strings.Contains(rendered, strings.ToLower(required)) {
			t.Errorf("catalog omitted %q, which a liaison needs to pick a profile: %s", required, rendered)
		}
	}
}

package mcpadapter

import (
	"context"
	"testing"
)

func TestFacadeCatalogIncludesCandidateApplicationMutation(t *testing.T) {
	facade, err := New(Config{
		Client: &fakeClient{}, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "operation-integration-mcp", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := connectFacade(t, facade).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tools.Tools {
		if listed.Name != "apply_integration_candidate" {
			continue
		}
		if listed.Annotations == nil || listed.Annotations.ReadOnlyHint ||
			listed.Annotations.DestructiveHint == nil || *listed.Annotations.DestructiveHint ||
			!listed.Annotations.IdempotentHint {
			t.Fatalf("integration tool annotations = %#v", listed.Annotations)
		}
		return
	}
	t.Fatal("apply_integration_candidate tool is absent")
}

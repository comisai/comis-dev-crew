package mcpadapter

import (
	"context"
	"testing"
)

func TestFacade_DoesNotExposeOperatorOnlyDiscard(t *testing.T) {
	client := &fakeClient{}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "discard_task" {
			t.Fatal("operator-only discard is exposed through MCP")
		}
	}
}

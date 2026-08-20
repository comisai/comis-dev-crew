package mcpadapter

import (
	"context"
	"testing"
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

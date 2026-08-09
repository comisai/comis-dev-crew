package domain

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestTaskPinBriefRevision_RendersOneCanonicalWorkerContract(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	task.AcceptanceCriteria = []string{
		"The focused behavior test passes.",
		"The complete repository verification remains green.",
	}
	task.Constraints = []string{
		"Do not invoke a shell for repository operations.",
		"Preserve unrelated changes.",
	}
	task.BriefRevisionHash = ""

	pinned, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	brief, err := pinned.RenderWorkerBrief()
	if err != nil {
		t.Fatalf("RenderWorkerBrief() error = %v", err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(brief.Content)))
	if brief.Revision != pinned.BriefRevision || brief.RevisionHash != wantDigest || pinned.BriefRevisionHash != wantDigest {
		t.Fatalf("brief pin = revision %d digest %q; task digest %q; want revision %d digest %q",
			brief.Revision, brief.RevisionHash, pinned.BriefRevisionHash, pinned.BriefRevision, wantDigest)
	}
	for _, required := range []string{
		"taskHandle: task-0001",
		"briefRevision: 1",
		"shape: ship",
		"deliveryMode: pull_request",
		"acceptanceCriteria:",
		"reportKinds: progress, attention, blocked, paused, candidate_complete, failed, resolution",
		"completionMeaning: candidate_complete requires service validation and evidence",
		"prohibitedActions: merge, mutate the primary checkout, change task shape, or bypass the reporter",
	} {
		if !strings.Contains(brief.Content, required) {
			t.Fatalf("brief content is missing %q:\n%s", required, brief.Content)
		}
	}

	second, err := pinned.RenderWorkerBrief()
	if err != nil {
		t.Fatalf("second RenderWorkerBrief() error = %v", err)
	}
	if second != brief {
		t.Fatalf("second rendering differs: got %#v want %#v", second, brief)
	}
}

func TestWorkerBriefValidate_RequiresExactBoundedRevisionPin(t *testing.T) {
	content := "taskHandle: task-0001\nbriefRevision: 1\n"
	valid := WorkerBrief{
		Revision: 1, RevisionHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), Content: content,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("WorkerBrief.Validate(valid) error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*WorkerBrief)
	}{
		{name: "revision", mutate: func(brief *WorkerBrief) { brief.Revision = 0 }},
		{name: "hash shape", mutate: func(brief *WorkerBrief) { brief.RevisionHash = "bad" }},
		{name: "empty content", mutate: func(brief *WorkerBrief) { brief.Content = "" }},
		{name: "oversized content", mutate: func(brief *WorkerBrief) { brief.Content = strings.Repeat("x", maximumWorkerBriefBytes+1) }},
		{name: "invalid UTF8", mutate: func(brief *WorkerBrief) { brief.Content = string([]byte{0xff}) }},
		{name: "nonline control", mutate: func(brief *WorkerBrief) { brief.Content = "task\thandle" }},
		{name: "changed content", mutate: func(brief *WorkerBrief) { brief.Content += "changed\n" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			brief := valid
			test.mutate(&brief)
			if err := brief.Validate(); err == nil {
				t.Fatal("WorkerBrief.Validate() error = nil, want rejection")
			}
		})
	}
}

func TestTaskRenderWorkerBrief_RejectsContractChangedAfterPin(t *testing.T) {
	task := validTask(ShapeShip, DeliveryPullRequest)
	task.AcceptanceCriteria = []string{"The requested behavior is proven."}
	task.Constraints = []string{"Keep evidence bounded."}
	task.BriefRevisionHash = ""
	pinned, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{name: "acceptance changed", mutate: func(task *Task) { task.AcceptanceCriteria[0] = "Different acceptance." }},
		{name: "constraint changed", mutate: func(task *Task) { task.Constraints = append(task.Constraints, "New constraint.") }},
		{name: "base changed", mutate: func(task *Task) { task.BaseRevision = strings.Repeat("b", 40) }},
		{name: "profile changed", mutate: func(task *Task) { task.WorkerProfileID = "fixture-worker" }},
		{name: "revision changed", mutate: func(task *Task) { task.BriefRevision++ }},
		{name: "digest changed", mutate: func(task *Task) { task.BriefRevisionHash = strings.Repeat("0", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := pinned
			changed.AcceptanceCriteria = append([]string(nil), pinned.AcceptanceCriteria...)
			changed.Constraints = append([]string(nil), pinned.Constraints...)
			test.mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatal("Task.Validate() error = nil, want stale brief pin rejection")
			}
			if _, err := changed.RenderWorkerBrief(); err == nil {
				t.Fatal("RenderWorkerBrief() error = nil, want stale brief pin rejection")
			}
		})
	}
}

func TestTaskPinBriefRevision_RejectsUnsafeOrAmbiguousContractText(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{name: "missing acceptance", mutate: func(task *Task) { task.AcceptanceCriteria = nil }},
		{name: "too many acceptance criteria", mutate: func(task *Task) {
			task.AcceptanceCriteria = make([]string, 33)
			for index := range task.AcceptanceCriteria {
				task.AcceptanceCriteria[index] = fmt.Sprintf("Criterion %d", index)
			}
		}},
		{name: "duplicate acceptance", mutate: func(task *Task) {
			task.AcceptanceCriteria = []string{"Same criterion", "Same criterion"}
		}},
		{name: "control character", mutate: func(task *Task) { task.Constraints = []string{"unsafe\nconstraint"} }},
		{name: "too many constraints", mutate: func(task *Task) {
			task.Constraints = make([]string, 33)
			for index := range task.Constraints {
				task.Constraints[index] = fmt.Sprintf("Constraint %d", index)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validTask(ShapeShip, DeliveryPullRequest)
			task.AcceptanceCriteria = []string{"The requested behavior is proven."}
			task.Constraints = []string{"Preserve unrelated work."}
			task.BriefRevisionHash = ""
			test.mutate(&task)
			if _, err := task.PinBriefRevision(); err == nil {
				t.Fatal("PinBriefRevision() error = nil, want contract validation failure")
			}
		})
	}
}

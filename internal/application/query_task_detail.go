package application

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ShowTask returns durable detail for one validated task handle.
func (queries *Queries) ShowTask(ctx context.Context, handle string) (TaskDetail, error) {
	if err := domain.ValidateTaskHandle(handle); err != nil {
		return TaskDetail{}, invalidReferenceFailure("task handle", err)
	}
	observation, err := queries.taskObservation(ctx, handle)
	if err != nil {
		return TaskDetail{}, translateReadError(err, "task")
	}
	task := observation.Task
	now := queries.now()
	summary, evidence, err := queries.projectTask(ctx, observation, now)
	if err != nil {
		return TaskDetail{}, err
	}
	replacement, err := queries.taskReplacementView(ctx, handle)
	if err != nil {
		return TaskDetail{}, err
	}
	promotion, err := queries.taskPromotionView(ctx, handle)
	if err != nil {
		return TaskDetail{}, err
	}
	return TaskDetail{
		SchemaVersion:     1,
		CapturedAtMs:      now.UnixMilli(),
		Completeness:      CompletenessPartial,
		Summary:           summary,
		Evidence:          evidence,
		Shape:             task.Shape,
		BaseRevision:      task.BaseRevision,
		BriefRevision:     task.BriefRevision,
		ValidationProfile: task.ValidationProfile,
		DeliveryMode:      task.DeliveryMode,
		ReportCursor:      task.ReportCursor,
		Replacement:       replacement,
		Promotion:         promotion,
		StateVersion:      task.StateVersion,
		CreatedAtMs:       task.CreatedAt.UnixMilli(),
		UpdatedAtMs:       task.UpdatedAt.UnixMilli(),
	}, nil
}

// taskReplacementView reads the most recent worker swap for one task. A task
// nobody replaced has no view, which is the ordinary case and not a failure.
func (queries *Queries) taskReplacementView(
	ctx context.Context,
	handle string,
) (*TaskReplacementView, error) {
	record, found, err := queries.repository.TaskReplacement(ctx, handle)
	if err != nil {
		return nil, translateReadError(err, "task replacement")
	}
	if !found {
		return nil, nil
	}
	return &TaskReplacementView{
		PreviousWorkerProfileID: record.PreviousWorkerProfileID,
		WorkerProfileID:         record.WorkerProfileID,
		HeadRevision:            record.HeadRevision,
		Cleanliness:             record.Cleanliness,
		BriefRevision:           record.BriefRevision,
		ObservedAtMs:            record.ObservedAt.UnixMilli(),
	}, nil
}

// taskPromotionView reads the scout one ship task was promoted from. Most ship
// tasks were written directly and have none, which is the ordinary case.
func (queries *Queries) taskPromotionView(
	ctx context.Context,
	handle string,
) (*TaskPromotionView, error) {
	link, found, err := queries.repository.ScoutPromotion(ctx, handle)
	if err != nil {
		return nil, translateReadError(err, "scout promotion")
	}
	if !found {
		return nil, nil
	}
	return &TaskPromotionView{
		ScoutTaskHandle: link.ScoutTaskHandle,
		EvidenceDigest:  link.EvidenceDigest,
		PromotedAtMs:    link.PromotedAt.UnixMilli(),
	}, nil
}

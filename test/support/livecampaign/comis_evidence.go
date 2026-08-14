package livecampaign

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type comisToolCount struct {
	OK     int `json:"ok"`
	Failed int `json:"failed"`
}

type comisSystemHealthReport struct {
	SchemaVersion int     `json:"schemaVersion"`
	WindowHours   float64 `json:"windowHours"`
	Sessions      struct {
		Total                   int      `json:"total"`
		Degraded                int      `json:"degraded"`
		DegradedRate            float64  `json:"degradedRate"`
		DeliveredWithToolErrors *int     `json:"deliveredWithToolErrors"`
		HardDegraded            *int     `json:"hardDegraded"`
		HardDegradedRate        *float64 `json:"hardDegradedRate"`
	} `json:"sessions"`
	TopErrorKinds []struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
	} `json:"topErrorKinds"`
	DegradedByCause  map[string]int            `json:"degradedByCause"`
	BreakerTripTotal int                       `json:"breakerTripTotal"`
	ToolStats        map[string]comisToolCount `json:"toolStats"`
	Cost             struct {
		CostUSD     float64 `json:"costUsd"`
		TotalTokens int64   `json:"totalTokens"`
	} `json:"cost"`
	Activity struct {
		ActiveAgents   []string       `json:"activeAgents"`
		ActiveChannels []string       `json:"activeChannels"`
		ExitReasons    map[string]int `json:"exitReasons"`
		TurnTotal      int            `json:"turnTotal"`
		TokenTotal     int64          `json:"tokenTotal"`
	} `json:"activity"`
	Findings []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Count  int    `json:"count"`
		Hint   string `json:"hint"`
	} `json:"findings"`
	LikelyRootCause    json.RawMessage `json:"likelyRootCause"`
	SuggestedNextSteps []string        `json:"suggestedNextSteps"`
	Truncations        []struct {
		Field  string `json:"field"`
		Reason string `json:"reason"`
	} `json:"truncations"`
	Coverage *struct {
		SessionSummary struct {
			Found bool `json:"found"`
			Rows  int  `json:"rows"`
		} `json:"sessionSummary"`
		SessionIndex struct {
			DaysRead    int `json:"daysRead"`
			DaysMissing int `json:"daysMissing"`
		} `json:"sessionIndex"`
		Billing struct {
			Present bool `json:"present"`
		} `json:"billing"`
	} `json:"coverage"`
}

type comisIncidentReport struct {
	SchemaVersion int    `json:"schemaVersion"`
	SessionKey    string `json:"sessionKey"`
	TraceID       string `json:"traceId"`
	AgentID       string `json:"agentId"`
	Channel       struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"channel"`
	Outcome struct {
		EndReason string `json:"endReason"`
		Degraded  bool   `json:"degraded"`
		Severity  string `json:"severity"`
	} `json:"outcome"`
	Cost struct {
		CostUSD        float64 `json:"costUsd"`
		TotalTokens    int64   `json:"totalTokens"`
		CacheReadRatio float64 `json:"cacheReadRatio"`
	} `json:"cost"`
	Timing struct {
		DurationMs int64 `json:"durationMs"`
		TurnCount  int   `json:"turnCount"`
	} `json:"timing"`
	ToolStats map[string]comisToolCount `json:"toolStats"`
	Failures  []struct {
		Seq          int    `json:"seq"`
		ToolName     string `json:"toolName"`
		ErrorKind    string `json:"errorKind"`
		FailureCode  string `json:"failureCode"`
		ErrorPreview string `json:"errorPreview"`
	} `json:"failures"`
	BreakerTimeline    []json.RawMessage `json:"breakerTimeline"`
	Offloads           []json.RawMessage `json:"offloads"`
	Summary            string            `json:"summary"`
	LikelyRootCause    json.RawMessage   `json:"likelyRootCause"`
	SuggestedNextSteps []string          `json:"suggestedNextSteps"`
	Truncations        []json.RawMessage `json:"truncations"`
	Coverage           *struct {
		Trajectory struct {
			Found   bool `json:"found"`
			Records int  `json:"records"`
		} `json:"trajectory"`
		Rollup struct {
			Present bool `json:"present"`
		} `json:"rollup"`
		Offloads struct {
			PointersResolved int `json:"pointersResolved"`
			PointersTotal    int `json:"pointersTotal"`
		} `json:"offloads"`
		LosslessContext *struct {
			Found       bool `json:"found"`
			ToolResults int  `json:"toolResults"`
		} `json:"losslessContext"`
	} `json:"coverage"`
}

var requiredCampaignToolCounts = map[string]comisToolCount{
	"prepare_task":    {OK: 2},
	"list_tasks":      {OK: 1},
	"get_task":        {OK: 2},
	"explain_task":    {OK: 1},
	"get_launch_plan": {OK: 2},
	"reconcile_task":  {OK: 1},
	"handback_task":   {OK: 1},
	"cleanup_task":    {OK: 2, Failed: 6},
}

func verifyComisSystemHealth(manifest Manifest, report comisSystemHealthReport) error {
	durationHours := float64(manifest.EndedAtMs-manifest.StartedAtMs) / float64(60*60*1000)
	if report.SchemaVersion != 1 || report.WindowHours < durationHours || report.Sessions.Total < len(manifest.Tasks) {
		return errors.New("system health identity, window, or session total is incomplete")
	}
	if report.Sessions.Degraded < 0 || report.Sessions.Degraded > report.Sessions.Total ||
		math.Abs(report.Sessions.DegradedRate-float64(report.Sessions.Degraded)/float64(report.Sessions.Total)) > 0.000001 {
		return errors.New("system health degraded session totals are inconsistent")
	}
	if report.Sessions.DeliveredWithToolErrors == nil || report.Sessions.HardDegraded == nil ||
		report.Sessions.HardDegradedRate == nil || *report.Sessions.HardDegraded != 0 || *report.Sessions.HardDegradedRate != 0 ||
		*report.Sessions.DeliveredWithToolErrors != report.Sessions.Degraded {
		return errors.New("system health does not prove zero hard-degraded sessions")
	}
	if report.TopErrorKinds == nil || report.DegradedByCause == nil || report.ToolStats == nil ||
		report.Findings == nil || report.SuggestedNextSteps == nil || report.Truncations == nil ||
		report.LikelyRootCause == nil || report.Activity.ExitReasons == nil ||
		report.Activity.ActiveAgents == nil || report.Activity.ActiveChannels == nil {
		return errors.New("system health required bounded sections are absent")
	}
	if report.BreakerTripTotal != 0 || report.Cost.CostUSD < 0 || report.Cost.TotalTokens < 0 ||
		report.Activity.TurnTotal < len(manifest.Tasks) || report.Activity.TokenTotal < 0 ||
		!containsString(report.Activity.ActiveAgents, manifest.Comis.AgentID) ||
		!containsString(report.Activity.ActiveChannels, "telegram") {
		return errors.New("system health activity, cost, or breaker evidence is invalid")
	}
	if report.Coverage == nil || !report.Coverage.SessionSummary.Found ||
		report.Coverage.SessionSummary.Rows < len(manifest.Tasks) || report.Coverage.SessionIndex.DaysRead <= 0 ||
		report.Coverage.SessionIndex.DaysMissing != 0 || !report.Coverage.Billing.Present {
		return errors.New("system health source coverage is incomplete")
	}
	return nil
}

func verifyComisIncident(manifest Manifest, report comisIncidentReport) error {
	if report.SchemaVersion != 1 || report.SessionKey == "" || report.TraceID == "" ||
		report.AgentID != manifest.Comis.AgentID || report.Channel.Type != "telegram" ||
		report.Channel.ID != manifest.Telegram.OriginChatID {
		return errors.New("session explanation identity or Telegram origin is invalid")
	}
	if report.Outcome.EndReason == "" || (report.Outcome.Severity != "ok" && report.Outcome.Severity != "degraded") ||
		report.Cost.CostUSD < 0 || report.Cost.TotalTokens < 0 || report.Cost.CacheReadRatio < 0 ||
		report.Cost.CacheReadRatio > 1 || report.Timing.DurationMs <= 0 || report.Timing.TurnCount <= 0 {
		return errors.New("session explanation outcome, cost, or timing is invalid")
	}
	if len(report.ToolStats) == 0 || report.Failures == nil ||
		report.BreakerTimeline == nil || report.Offloads == nil || strings.TrimSpace(report.Summary) == "" ||
		report.LikelyRootCause == nil || report.SuggestedNextSteps == nil || report.Truncations == nil {
		return errors.New("session explanation required bounded sections are absent")
	}
	if report.Coverage == nil || !report.Coverage.Rollup.Present ||
		report.Coverage.Offloads.PointersResolved != report.Coverage.Offloads.PointersTotal {
		return errors.New("session explanation source coverage is incomplete")
	}
	trajectoryCovered := report.Coverage.Trajectory.Found && report.Coverage.Trajectory.Records > 0
	losslessCovered := report.Coverage.LosslessContext != nil && report.Coverage.LosslessContext.Found &&
		report.Coverage.LosslessContext.ToolResults > 0
	if !trajectoryCovered && !losslessCovered {
		return errors.New("session explanation has no authoritative trajectory or lossless tool evidence")
	}
	return nil
}

func verifyComisCampaignTools(reports []comisIncidentReport) error {
	observed := make(map[string]comisToolCount, len(requiredCampaignToolCounts))
	cleanupPreconditions := 0
	requiredCleanupReasons := []string{
		application.CleanupOpenDecisionMessage,
		application.CleanupOpenHoldMessage,
		application.CleanupActiveExecutionMessage,
		application.CleanupUnknownExecutionMessage,
		application.CleanupDirtyWorkspaceMessage,
		application.CleanupStaleForgeTruthMessage,
	}
	observedCleanupReasons := make(map[string]bool, len(requiredCleanupReasons))
	for _, report := range reports {
		for name, counts := range report.ToolStats {
			if counts.OK < 0 || counts.Failed < 0 {
				return errors.New("required Comis tool evidence contains a negative count")
			}
			total := observed[name]
			total.OK += counts.OK
			total.Failed += counts.Failed
			observed[name] = total
		}
		for _, failure := range report.Failures {
			if failure.ToolName == "cleanup_task" && failure.FailureCode == "precondition" {
				cleanupPreconditions++
				for _, reason := range requiredCleanupReasons {
					if strings.Contains(failure.ErrorPreview, reason) {
						observedCleanupReasons[reason] = true
					}
				}
			}
		}
	}
	for name, minimum := range requiredCampaignToolCounts {
		counts := observed[name]
		if counts.OK < minimum.OK || counts.Failed < minimum.Failed {
			return errors.New("required Comis tool evidence is incomplete")
		}
	}
	if cleanupPreconditions < len(requiredCleanupReasons) {
		return errors.New("required Comis cleanup refusal evidence is incomplete")
	}
	for _, reason := range requiredCleanupReasons {
		if !observedCleanupReasons[reason] {
			return errors.New("required Comis cleanup refusal reasons are incomplete")
		}
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

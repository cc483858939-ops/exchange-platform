package tasks

import (
	"strings"

	"Go.exchange/eventing"
)

func validRecommendationMetricsPayload(eventType string, payload eventing.RecommendationBehaviorPayload) bool {
	if eventing.ValidateRecommendationProvenance(payload.ExplorationOpportunity, payload.SelectionMode, payload.ExplorationReason) != nil {
		return false
	}
	switch eventType {
	case eventing.EventTypeRecommendationImpression, eventing.EventTypeRecommendationClick, eventing.EventTypeRecommendationNotInterested:
		return payload.ForegroundTimeMS == nil &&
			payload.ScrollProgressPercent == nil &&
			payload.ExitType == nil &&
			payload.EstimatedReadTimeMS == nil &&
			payload.ReadPolicyVersion == nil &&
			payload.ReadOutcome == nil &&
			payload.FeedVisibleTimeMS == nil
	case eventing.EventTypeRecommendationReadEnd:
		if payload.ForegroundTimeMS == nil || payload.ScrollProgressPercent == nil || payload.ExitType == nil ||
			payload.EstimatedReadTimeMS == nil || payload.ReadPolicyVersion == nil || payload.ReadOutcome == nil ||
			payload.FeedVisibleTimeMS != nil ||
			*payload.ForegroundTimeMS < 0 || *payload.ForegroundTimeMS > 6*60*60*1000 ||
			*payload.ScrollProgressPercent < 0 || *payload.ScrollProgressPercent > 100 ||
			*payload.EstimatedReadTimeMS <= 0 || strings.TrimSpace(*payload.ReadPolicyVersion) == "" ||
			!validRecommendationMetricsExitType(*payload.ExitType) ||
			!validRecommendationMetricsReadOutcome(*payload.ReadOutcome) {
			return false
		}
		return *payload.ReadPolicyVersion == "read_v1"
	case eventing.EventTypeRecommendationFeedDwell:
		if payload.FeedVisibleTimeMS == nil || *payload.FeedVisibleTimeMS < 1 || *payload.FeedVisibleTimeMS > 6*60*60*1000 {
			return false
		}
		return payload.ForegroundTimeMS == nil &&
			payload.ScrollProgressPercent == nil &&
			payload.ExitType == nil &&
			payload.EstimatedReadTimeMS == nil &&
			payload.ReadPolicyVersion == nil &&
			payload.ReadOutcome == nil
	default:
		return false
	}
}

func validRecommendationMetricsReadOutcome(outcome string) bool {
	switch outcome {
	case "qualified", "quick_bounce", "neutral":
		return true
	default:
		return false
	}
}

func validRecommendationMetricsExitType(exitType string) bool {
	switch exitType {
	case "back_to_recommendation", "navigate_to_article", "route_leave", "page_hide", "refresh", "unknown":
		return true
	default:
		return false
	}
}

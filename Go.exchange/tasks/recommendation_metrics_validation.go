package tasks

import (
	"strings"

	"Go.exchange/eventing"
	"Go.exchange/models"
)

func validRecommendationMetricsFact(fact eventing.RecommendationEventFact) bool {
	switch fact.EventType {
	case models.RecommendationEventTypeImpression, models.RecommendationEventTypeClick, models.RecommendationEventTypeNotInterested:
		return fact.ForegroundTimeMS == nil &&
			fact.ScrollProgressPercent == nil &&
			fact.ExitType == nil &&
			fact.EstimatedReadTimeMS == nil &&
			fact.ReadPolicyVersion == nil &&
			fact.ReadOutcome == nil &&
			fact.FeedVisibleTimeMS == nil
	case models.RecommendationEventTypeReadEnd:
		if fact.ForegroundTimeMS == nil || fact.ScrollProgressPercent == nil || fact.ExitType == nil ||
			fact.EstimatedReadTimeMS == nil || fact.ReadPolicyVersion == nil || fact.ReadOutcome == nil ||
			fact.FeedVisibleTimeMS != nil ||
			*fact.ForegroundTimeMS < 0 || *fact.ForegroundTimeMS > 6*60*60*1000 ||
			*fact.ScrollProgressPercent < 0 || *fact.ScrollProgressPercent > 100 ||
			*fact.EstimatedReadTimeMS <= 0 || strings.TrimSpace(*fact.ReadPolicyVersion) == "" ||
			!validRecommendationMetricsExitType(*fact.ExitType) ||
			!validRecommendationMetricsReadOutcome(*fact.ReadOutcome) {
			return false
		}
		return *fact.ReadPolicyVersion == "read_v1"
	case models.RecommendationEventTypeFeedDwell:
		if fact.FeedVisibleTimeMS == nil || *fact.FeedVisibleTimeMS < 1 || *fact.FeedVisibleTimeMS > 6*60*60*1000 {
			return false
		}
		return fact.ForegroundTimeMS == nil &&
			fact.ScrollProgressPercent == nil &&
			fact.ExitType == nil &&
			fact.EstimatedReadTimeMS == nil &&
			fact.ReadPolicyVersion == nil &&
			fact.ReadOutcome == nil
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

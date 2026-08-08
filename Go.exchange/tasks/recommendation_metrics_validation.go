package tasks

import (
	"Go.exchange/eventing"
	"Go.exchange/models"
)

func validRecommendationMetricsFact(fact eventing.RecommendationEventFact) bool {
	switch fact.EventType {
	case models.RecommendationEventTypeImpression, models.RecommendationEventTypeClick, models.RecommendationEventTypeNotInterested:
		return fact.ForegroundTimeMS == nil && fact.MaxScrollDepth == nil && fact.ExitType == nil &&
			!fact.QualifiedRead && !fact.QuickBounce
	case models.RecommendationEventTypeReadEnd:
		if fact.ForegroundTimeMS == nil || fact.MaxScrollDepth == nil || fact.ExitType == nil ||
			*fact.ForegroundTimeMS < 0 || *fact.ForegroundTimeMS > 6*60*60*1000 ||
			*fact.MaxScrollDepth < 0 || *fact.MaxScrollDepth > 100 ||
			fact.QualifiedRead && fact.QuickBounce {
			return false
		}
		return validRecommendationMetricsExitType(*fact.ExitType)
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

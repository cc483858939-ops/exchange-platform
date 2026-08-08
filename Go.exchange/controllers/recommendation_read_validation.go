package controllers

import (
	"strings"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"
)

func isRecommendationEventType(eventType string) bool {
	switch eventType {
	case models.RecommendationEventTypeImpression, models.RecommendationEventTypeClick,
		models.RecommendationEventTypeReadEnd, models.RecommendationEventTypeNotInterested:
		return true
	default:
		return false
	}
}

func validateRecommendationReadPayload(eventType string, input recommendationEventInput) (*int64, *int, *string, bool, bool, string) {
	if eventType != models.RecommendationEventTypeReadEnd {
		if input.ForegroundTimeMS != nil || input.MaxScrollDepth != nil || input.ExitType != nil {
			return nil, nil, nil, false, false, "unexpected_read_payload"
		}
		return nil, nil, nil, false, false, ""
	}

	if input.ForegroundTimeMS == nil || input.MaxScrollDepth == nil || input.ExitType == nil {
		return nil, nil, nil, false, false, "missing_read_payload"
	}
	foregroundTimeMS := *input.ForegroundTimeMS
	if foregroundTimeMS < 0 || foregroundTimeMS > 6*time.Hour.Milliseconds() {
		return nil, nil, nil, false, false, "invalid_foreground_time_ms"
	}
	maxScrollDepth := *input.MaxScrollDepth
	if maxScrollDepth < 0 || maxScrollDepth > 100 {
		return nil, nil, nil, false, false, "invalid_max_scroll_depth"
	}
	exitTypeValue := strings.TrimSpace(*input.ExitType)
	if !isRecommendationExitType(exitTypeValue) {
		return nil, nil, nil, false, false, "invalid_exit_type"
	}

	exitType := exitTypeValue
	qualifiedRead := time.Duration(foregroundTimeMS)*time.Millisecond >= config.RecommendationQualifiedReadTime() ||
		maxScrollDepth >= config.RecommendationQualifiedScrollPercent()
	quickBounce := time.Duration(foregroundTimeMS)*time.Millisecond < config.RecommendationQuickBounceTime() &&
		maxScrollDepth < config.RecommendationQuickBounceScrollPercent()
	if qualifiedRead {
		quickBounce = false
	}
	return &foregroundTimeMS, &maxScrollDepth, &exitType, qualifiedRead, quickBounce, ""
}

func isRecommendationExitType(exitType string) bool {
	switch exitType {
	case "back_to_recommendation", "navigate_to_article", "route_leave", "page_hide", "refresh", "unknown":
		return true
	default:
		return false
	}
}

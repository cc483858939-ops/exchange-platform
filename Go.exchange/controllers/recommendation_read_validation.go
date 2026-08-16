package controllers

import (
	"strings"

	"Go.exchange/models"
)

func isRecommendationEventType(eventType string) bool {
	switch eventType {
	case models.RecommendationEventTypeImpression, models.RecommendationEventTypeClick,
		models.RecommendationEventTypeReadEnd, models.RecommendationEventTypeFeedDwell, models.RecommendationEventTypeNotInterested:
		return true
	default:
		return false
	}
}

func validateRecommendationReadPayload(eventType string, input recommendationEventInput, estimatedReadTimeMS int64, readPolicyVersion string) (*int64, *int, *string, *int64, *string, *string, string) {
	if eventType != models.RecommendationEventTypeReadEnd {
		if input.ForegroundTimeMS != nil || input.ScrollProgressPercent != nil || input.ExitType != nil {
			return nil, nil, nil, nil, nil, nil, "unexpected_read_payload"
		}
		return nil, nil, nil, nil, nil, nil, ""
	}

	if input.ForegroundTimeMS == nil || input.ScrollProgressPercent == nil || input.ExitType == nil {
		return nil, nil, nil, nil, nil, nil, "missing_read_payload"
	}
	foregroundTimeMS := *input.ForegroundTimeMS
	if foregroundTimeMS < 0 || foregroundTimeMS > recommendationReadMaxForegroundMS {
		return nil, nil, nil, nil, nil, nil, "invalid_foreground_time_ms"
	}
	scrollProgressPercent := *input.ScrollProgressPercent
	if scrollProgressPercent < 0 || scrollProgressPercent > recommendationReadMaxProgress {
		return nil, nil, nil, nil, nil, nil, "invalid_scroll_progress_percent"
	}
	exitTypeValue := strings.TrimSpace(*input.ExitType)
	if !isRecommendationExitType(exitTypeValue) {
		return nil, nil, nil, nil, nil, nil, "invalid_exit_type"
	}
	if estimatedReadTimeMS <= 0 {
		return nil, nil, nil, nil, nil, nil, "invalid_estimated_read_time_ms"
	}
	if strings.TrimSpace(readPolicyVersion) != recommendationReadPolicyVersion {
		return nil, nil, nil, nil, nil, nil, "unsupported_read_policy_version"
	}

	readOutcome, err := classifyRecommendationRead(foregroundTimeMS, scrollProgressPercent, estimatedReadTimeMS, readPolicyVersion)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, "invalid_read_classification"
	}
	exitType := exitTypeValue
	policyVersion := readPolicyVersion
	outcome := readOutcome
	estimated := estimatedReadTimeMS
	return &foregroundTimeMS, &scrollProgressPercent, &exitType, &estimated, &policyVersion, &outcome, ""
}

func isRecommendationExitType(exitType string) bool {
	switch exitType {
	case "back_to_recommendation", "navigate_to_article", "route_leave", "page_hide", "refresh", "unknown":
		return true
	default:
		return false
	}
}

const recommendationFeedDwellMaxVisibleTimeMS int64 = 6 * 60 * 60 * 1000

func validateRecommendationFeedPayload(eventType string, input recommendationEventInput) (*int64, string) {
	if eventType != models.RecommendationEventTypeFeedDwell {
		if input.FeedVisibleTimeMS != nil {
			return nil, "unexpected_feed_payload"
		}
		return nil, ""
	}
	if input.ForegroundTimeMS != nil || input.ScrollProgressPercent != nil || input.ExitType != nil {
		return nil, "unexpected_feed_payload"
	}
	if input.FeedVisibleTimeMS == nil {
		return nil, "missing_feed_payload"
	}
	value := *input.FeedVisibleTimeMS
	if value < 1 || value > recommendationFeedDwellMaxVisibleTimeMS {
		return nil, "invalid_feed_visible_time_ms"
	}
	return &value, ""
}

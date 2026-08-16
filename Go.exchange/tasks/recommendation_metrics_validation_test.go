package tasks

import (
	"testing"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/models"
)

func TestValidRecommendationMetricsFactFeedDwell(t *testing.T) {
	duration := int64(8234)
	now := time.Now().UTC()
	base := eventing.RecommendationEventFact{
		EventID: "event", UserID: 7, RequestID: "request", ArticleID: 11,
		Scene: "recommendation_page", Position: 1, RankerVersion: "rules_v3",
		RankerConfigHash: "0123456789ab", StrategyID: "personalized_rules_v3",
		OccurredAt: now, ReceivedAt: now, EventType: models.RecommendationEventTypeFeedDwell,
		FeedVisibleTimeMS: &duration,
	}
	if !validRecommendationMetricsFact(base) {
		t.Fatal("valid feed dwell fact rejected")
	}

	tests := []struct {
		name   string
		mutate func(*eventing.RecommendationEventFact)
	}{
		{name: "missing duration", mutate: func(f *eventing.RecommendationEventFact) { f.FeedVisibleTimeMS = nil }},
		{name: "zero duration", mutate: func(f *eventing.RecommendationEventFact) { value := int64(0); f.FeedVisibleTimeMS = &value }},
		{name: "negative duration", mutate: func(f *eventing.RecommendationEventFact) { value := int64(-1); f.FeedVisibleTimeMS = &value }},
		{name: "over six hours", mutate: func(f *eventing.RecommendationEventFact) {
			value := int64(6*60*60*1000 + 1)
			f.FeedVisibleTimeMS = &value
		}},
		{name: "read payload", mutate: func(f *eventing.RecommendationEventFact) { value := int64(1000); f.ForegroundTimeMS = &value }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fact := base
			tc.mutate(&fact)
			if validRecommendationMetricsFact(fact) {
				t.Fatal("malformed feed dwell fact accepted")
			}
		})
	}
}

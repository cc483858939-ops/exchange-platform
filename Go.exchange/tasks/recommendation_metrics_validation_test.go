package tasks

import (
	"testing"
	"time"

	"Go.exchange/eventing"
)

func TestValidRecommendationMetricsPayloadFeedDwell(t *testing.T) {
	duration := int64(8234)
	base := eventing.RecommendationBehaviorPayload{FeedVisibleTimeMS: &duration}
	if !validRecommendationMetricsPayload(eventing.EventTypeRecommendationFeedDwell, base) {
		t.Fatal("valid feed dwell payload rejected")
	}

	tests := []struct {
		name   string
		mutate func(*eventing.RecommendationBehaviorPayload)
	}{
		{name: "missing duration", mutate: func(payload *eventing.RecommendationBehaviorPayload) { payload.FeedVisibleTimeMS = nil }},
		{name: "zero duration", mutate: func(payload *eventing.RecommendationBehaviorPayload) {
			value := int64(0)
			payload.FeedVisibleTimeMS = &value
		}},
		{name: "negative duration", mutate: func(payload *eventing.RecommendationBehaviorPayload) {
			value := int64(-1)
			payload.FeedVisibleTimeMS = &value
		}},
		{name: "over six hours", mutate: func(payload *eventing.RecommendationBehaviorPayload) {
			value := int64(6*60*60*1000 + 1)
			payload.FeedVisibleTimeMS = &value
		}},
		{name: "read payload", mutate: func(payload *eventing.RecommendationBehaviorPayload) {
			value := int64(1000)
			payload.ForegroundTimeMS = &value
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := base
			tc.mutate(&payload)
			if validRecommendationMetricsPayload(eventing.EventTypeRecommendationFeedDwell, payload) {
				t.Fatal("malformed feed dwell payload accepted")
			}
		})
	}
}

func TestValidRecommendationMetricsPayloadReadEnd(t *testing.T) {
	foreground, progress, estimated := int64(1000), 100, int64(3000)
	exitType, policy, outcome := "route_leave", "read_v1", "qualified"
	payload := eventing.RecommendationBehaviorPayload{
		ForegroundTimeMS: &foreground, ScrollProgressPercent: &progress, ExitType: &exitType,
		EstimatedReadTimeMS: &estimated, ReadPolicyVersion: &policy, ReadOutcome: &outcome,
	}
	if !validRecommendationMetricsPayload(eventing.EventTypeRecommendationReadEnd, payload) {
		t.Fatal("valid read_end payload rejected")
	}
	_ = time.Now()
}

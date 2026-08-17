package tasks

import (
	"errors"
	"testing"
	"time"

	"Go.exchange/eventing"

	"github.com/google/uuid"
)

func TestApplyRecommendationMetricsEventClassifiesInvalidPayload(t *testing.T) {
	err := applyRecommendationMetricsEvent(eventing.Envelope{Payload: []byte("{\"user_id\":")})
	if !errors.Is(err, errInvalidRecommendationMetricsEvent) {
		t.Fatalf("expected invalid payload error, got %v", err)
	}
}

func TestAggregateRecommendationMetricsPreservesFullMetricDimensions(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	base := eventing.RecommendationBehaviorPayload{
		UserID: 7, ArticleID: 42, RequestID: uuid.NewString(),
		Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v3", RankerConfigHash: "config-a",
		StrategyID: "strategy-a", ReceivedAt: now,
	}
	records := make([]recommendationMetricEvent, 0, 4)
	for _, variant := range []struct {
		position int
		strategy string
		at       time.Time
	}{
		{position: 1, strategy: "strategy-a", at: now},
		{position: 2, strategy: "strategy-a", at: now},
		{position: 1, strategy: "strategy-b", at: now},
		{position: 1, strategy: "strategy-a", at: now.AddDate(0, 0, 1)},
	} {
		payload := base
		payload.Position = variant.position
		payload.StrategyID = variant.strategy
		payload.ReceivedAt = variant.at
		records = append(records, recommendationMetricEvent{
			Envelope: eventing.Envelope{
				ID: uuid.NewString(), Type: eventing.EventTypeRecommendationImpression, OccurredAt: variant.at,
			},
			Payload: payload,
		})

	}
	firstDelivery := make(map[string]struct{}, len(records))
	for _, record := range records {
		firstDelivery[record.Envelope.ID] = struct{}{}
	}
	aggregates := aggregateRecommendationMetrics(records, firstDelivery)
	if len(aggregates) != 4 {
		t.Fatalf("aggregates=%#v want=4", aggregates)
	}
}

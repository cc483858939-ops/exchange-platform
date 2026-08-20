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
		RankerVersion: "embedding_v1", RankerConfigHash: "config-a",
		StrategyID: "strategy-a", ReceivedAt: now, SelectionMode: eventing.RecommendationSelectionModeRanked,
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

func TestAggregateRecommendationMetricsSeparatesExplorationProvenanceDimensions(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	base := eventing.RecommendationBehaviorPayload{
		UserID: 7, ArticleID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v4", RankerConfigHash: "config-a", StrategyID: "strategy-a", ReceivedAt: now,
		SelectionMode: eventing.RecommendationSelectionModeRanked,
	}
	variants := []eventing.RecommendationBehaviorPayload{base, {
		UserID: 7, ArticleID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v4", RankerConfigHash: "config-a", StrategyID: "strategy-a", ReceivedAt: now,
		ExplorationOpportunity: true, SelectionMode: eventing.RecommendationSelectionModeExploration, ExplorationReason: eventing.RecommendationExplorationReasonRecent,
	}}
	records := make([]recommendationMetricEvent, 0, len(variants))
	firstDelivery := make(map[string]struct{}, len(variants))
	for _, payload := range variants {
		record := recommendationMetricEvent{Envelope: eventing.Envelope{ID: uuid.NewString(), Type: eventing.EventTypeRecommendationImpression, OccurredAt: now}, Payload: payload}
		records = append(records, record)
		firstDelivery[record.Envelope.ID] = struct{}{}
	}
	aggregates := aggregateRecommendationMetrics(records, firstDelivery)
	if len(aggregates) != 2 || aggregates[0].Key.SelectionMode == aggregates[1].Key.SelectionMode {
		t.Fatalf("aggregates=%#v, exploration provenance must be a metric dimension", aggregates)
	}
}

func TestAggregateRecommendationMetricsSeparatesAllThreeProvenanceStates(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	base := eventing.RecommendationBehaviorPayload{
		UserID: 7, ArticleID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v4", RankerConfigHash: "config-a", StrategyID: "strategy-a", ReceivedAt: now,
	}
	variants := []eventing.RecommendationBehaviorPayload{
		{UserID: base.UserID, ArticleID: base.ArticleID, RequestID: base.RequestID, Scene: base.Scene, Position: base.Position, RankerVersion: base.RankerVersion, RankerConfigHash: base.RankerConfigHash, StrategyID: base.StrategyID, ReceivedAt: base.ReceivedAt, SelectionMode: eventing.RecommendationSelectionModeRanked},
		{UserID: base.UserID, ArticleID: base.ArticleID, RequestID: base.RequestID, Scene: base.Scene, Position: base.Position, RankerVersion: base.RankerVersion, RankerConfigHash: base.RankerConfigHash, StrategyID: base.StrategyID, ReceivedAt: base.ReceivedAt, ExplorationOpportunity: true, SelectionMode: eventing.RecommendationSelectionModeRanked},
		{UserID: base.UserID, ArticleID: base.ArticleID, RequestID: base.RequestID, Scene: base.Scene, Position: base.Position, RankerVersion: base.RankerVersion, RankerConfigHash: base.RankerConfigHash, StrategyID: base.StrategyID, ReceivedAt: base.ReceivedAt, ExplorationOpportunity: true, SelectionMode: eventing.RecommendationSelectionModeExploration, ExplorationReason: eventing.RecommendationExplorationReasonRecent},
	}
	records := make([]recommendationMetricEvent, 0, len(variants))
	firstDelivery := make(map[string]struct{}, len(variants))
	for _, payload := range variants {
		record := recommendationMetricEvent{Envelope: eventing.Envelope{ID: uuid.NewString(), Type: eventing.EventTypeRecommendationImpression, OccurredAt: now}, Payload: payload}
		records = append(records, record)
		firstDelivery[record.Envelope.ID] = struct{}{}
	}
	aggregates := aggregateRecommendationMetrics(records, firstDelivery)
	if len(aggregates) != 3 {
		t.Fatalf("aggregates=%#v want three provenance dimensions", aggregates)
	}
	seen := make(map[recommendationMetricKey]struct{}, len(aggregates))
	for _, aggregate := range aggregates {
		seen[aggregate.Key] = struct{}{}
	}
	if len(seen) != 3 {
		t.Fatalf("metric keys=%v want three distinct keys", seen)
	}
}

func TestAggregateRecommendationMetricsSeparatesExplorationReasons(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	reasons := []string{
		eventing.RecommendationExplorationReasonRecent,
		eventing.RecommendationExplorationReasonNovelAuthor,
		eventing.RecommendationExplorationReasonRecentNovelAuthor,
	}
	records := make([]recommendationMetricEvent, 0, len(reasons))
	firstDelivery := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		record := recommendationMetricEvent{
			Envelope: eventing.Envelope{ID: uuid.NewString(), Type: eventing.EventTypeRecommendationImpression, OccurredAt: now},
			Payload: eventing.RecommendationBehaviorPayload{
				UserID: 7, ArticleID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
				RankerVersion: "rules_v4", RankerConfigHash: "config-a", StrategyID: "strategy-a", ReceivedAt: now,
				ExplorationOpportunity: true, SelectionMode: eventing.RecommendationSelectionModeExploration, ExplorationReason: reason,
			},
		}
		records = append(records, record)
		firstDelivery[record.Envelope.ID] = struct{}{}
	}
	aggregates := aggregateRecommendationMetrics(records, firstDelivery)
	if len(aggregates) != len(reasons) {
		t.Fatalf("aggregates=%#v want one row per reason", aggregates)
	}
	seen := make(map[string]struct{}, len(aggregates))
	for _, aggregate := range aggregates {
		seen[aggregate.Key.ExplorationReason] = struct{}{}
	}
	if len(seen) != len(reasons) {
		t.Fatalf("reason dimensions=%v want=%v", seen, reasons)
	}
}

func TestAggregateRecommendationBehaviorIgnoresSelectionProvenance(t *testing.T) {
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	base := eventing.RecommendationBehaviorPayload{
		UserID: 7, ArticleID: 42, RequestID: uuid.NewString(), Scene: "recommendation_page", Position: 1,
		RankerVersion: "rules_v4", RankerConfigHash: "config-a", StrategyID: "strategy-a", ReceivedAt: now,
	}
	records := []recommendationMetricEvent{}
	firstDelivery := make(map[string]struct{})
	for _, provenance := range []struct {
		opportunity bool
		mode        string
		reason      string
	}{
		{false, eventing.RecommendationSelectionModeRanked, ""},
		{true, eventing.RecommendationSelectionModeExploration, eventing.RecommendationExplorationReasonRecent},
	} {
		payload := base
		payload.ExplorationOpportunity = provenance.opportunity
		payload.SelectionMode = provenance.mode
		payload.ExplorationReason = provenance.reason
		record := recommendationMetricEvent{Envelope: eventing.Envelope{ID: uuid.NewString(), Type: eventing.EventTypeRecommendationClick, OccurredAt: now}, Payload: payload}
		records = append(records, record)
		firstDelivery[record.Envelope.ID] = struct{}{}
	}
	aggregates := aggregateRecommendationBehavior(records, firstDelivery)
	if len(aggregates) != 1 || aggregates[0].Key.Action != eventing.RecommendationBehaviorActionClick || aggregates[0].Count != 2 {
		t.Fatalf("behavior aggregates=%#v want one provenance-independent click row count=2", aggregates)
	}
}

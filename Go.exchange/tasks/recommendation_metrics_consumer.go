package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/metrics"
	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const recommendationMetricsConsumerRetryDelay = 2 * time.Second

var errInvalidRecommendationMetricsEvent = errors.New("invalid recommendation metrics event")

func startRecommendationMetricsConsumer(ctx context.Context, wg *sync.WaitGroup) {
	if config.AppConfig == nil ||
		strings.TrimSpace(config.AppConfig.Kafka.RecommendationEventsTopic) == "" ||
		strings.TrimSpace(config.AppConfig.Kafka.RecommendationMetricsGroupID) == "" {
		return
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			runRecommendationMetricsConsumer(ctx)
			if ctx.Err() != nil {
				return
			}
			log.Printf("[RecommendationMetrics] consumer stopped; retrying in %s", recommendationMetricsConsumerRetryDelay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(recommendationMetricsConsumerRetryDelay):
			}
		}
	}()
}

func runRecommendationMetricsConsumer(ctx context.Context) {
	reader, err := eventing.NewKafkaReader(
		config.AppConfig.Kafka,
		config.AppConfig.Kafka.RecommendationEventsTopic,
		config.AppConfig.Kafka.RecommendationMetricsGroupID,
	)
	if err != nil {
		log.Printf("[RecommendationMetrics] create Kafka reader: %v", err)
		return
	}
	recommendationMetricsConsumers.Add(1)
	defer recommendationMetricsConsumers.Add(-1)
	defer reader.Close()
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[RecommendationMetrics] fetch Kafka message: %v", err)
			}
			return
		}
		event, err := eventing.DecodeEnvelope(message.Value)
		if err != nil {
			metrics.RecordRecommendationTelemetryProjection("malformed_envelope")
			log.Printf("[RecommendationMetrics] discard malformed envelope: %v", err)
		} else if event.Type != eventing.EventTypeRecommendationEventsRecorded {
			metrics.RecordRecommendationTelemetryProjection("ignored_event_type")
		} else if event.SchemaVersion != 1 {
			metrics.RecordRecommendationTelemetryProjection("unsupported_schema")
			log.Printf("[RecommendationMetrics] discard event %s with schema version %d", event.ID, event.SchemaVersion)
		} else if err := applyRecommendationMetricsEvent(event); err != nil {
			if errors.Is(err, errInvalidRecommendationMetricsEvent) {
				metrics.RecordRecommendationTelemetryProjection("invalid_payload")
				log.Printf("[RecommendationMetrics] discard invalid event %s: %v", event.ID, err)
			} else {
				metrics.RecordRecommendationTelemetryProjection("retryable_error")
				log.Printf("[RecommendationMetrics] apply event %s: %v", event.ID, err)
				return
			}
		} else {
			metrics.RecordRecommendationTelemetryProjection("applied")
		}
		if err := reader.CommitMessages(ctx, message); err != nil {
			if ctx.Err() == nil {
				log.Printf("[RecommendationMetrics] commit Kafka message: %v", err)
			}
			return
		}
	}
}

func applyRecommendationMetricsEvent(event eventing.Envelope) error {
	var payload eventing.RecommendationEventsRecordedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("%w: decode payload: %v", errInvalidRecommendationMetricsEvent, err)
	}
	if payload.UserID == 0 || len(payload.Events) == 0 {
		return fmt.Errorf("%w: payload requires user and events", errInvalidRecommendationMetricsEvent)
	}
	for _, fact := range payload.Events {
		if fact.UserID != payload.UserID || fact.EventID == "" || fact.ArticleID == 0 || fact.RequestID == "" ||
			fact.Position <= 0 || fact.Scene == "" || fact.RankerVersion == "" || fact.RankerConfigHash == "" || fact.StrategyID == "" ||
			!validRecommendationMetricsFact(fact) || fact.OccurredAt.IsZero() {
			return fmt.Errorf("%w: invalid fact %q", errInvalidRecommendationMetricsEvent, fact.EventID)
		}
	}
	if global.Db == nil {
		return errors.New("database is not initialized")
	}

	return global.Db.Transaction(func(tx *gorm.DB) error {
		firstDelivery, err := eventing.MarkInboxProcessed(tx, config.AppConfig.Kafka.RecommendationMetricsGroupID, event.ID)
		if err != nil || !firstDelivery {
			return err
		}
		for _, fact := range payload.Events {
			if err := upsertRecommendationDailyMetric(tx, fact); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsertRecommendationDailyMetric(tx *gorm.DB, fact eventing.RecommendationEventFact) error {
	occurredAt := fact.OccurredAt.UTC()
	metricDate := time.Date(occurredAt.Year(), occurredAt.Month(), occurredAt.Day(), 0, 0, 0, 0, time.UTC)
	impressions, clicks, qualifiedReads, quickBounces, notInterested := int64(0), int64(0), int64(0), int64(0), int64(0)
	feedDwellCount, feedVisibleTimeMS := int64(0), int64(0)
	switch fact.EventType {
	case models.RecommendationEventTypeImpression:
		impressions = 1
	case models.RecommendationEventTypeClick:
		clicks = 1
	case models.RecommendationEventTypeReadEnd:
		if fact.ReadOutcome != nil {
			switch *fact.ReadOutcome {
			case "qualified":
				qualifiedReads = 1
			case "quick_bounce":
				quickBounces = 1
			}
		}
	case models.RecommendationEventTypeNotInterested:
		notInterested = 1
	case models.RecommendationEventTypeFeedDwell:
		feedDwellCount = 1
		if fact.FeedVisibleTimeMS != nil {
			feedVisibleTimeMS = *fact.FeedVisibleTimeMS
		}
	}
	metric := models.RecommendationDailyMetric{
		MetricDate: metricDate, Scene: fact.Scene, RankerVersion: fact.RankerVersion,
		RankerConfigHash: fact.RankerConfigHash, StrategyID: fact.StrategyID,
		Position: fact.Position, ArticleID: fact.ArticleID,
		ImpressionCount: impressions, ClickCount: clicks, QualifiedReadCount: qualifiedReads,
		QuickBounceCount: quickBounces, NotInterestedCount: notInterested,
		FeedDwellCount: feedDwellCount, FeedVisibleTimeMS: feedVisibleTimeMS, UpdatedAt: time.Now().UTC(),
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "metric_date"}, {Name: "scene"}, {Name: "ranker_version"},
			{Name: "ranker_config_hash"}, {Name: "strategy_id"}, {Name: "position"}, {Name: "article_id"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"impression_count":     gorm.Expr("recommendation_daily_metrics.impression_count + ?", impressions),
			"click_count":          gorm.Expr("recommendation_daily_metrics.click_count + ?", clicks),
			"qualified_read_count": gorm.Expr("recommendation_daily_metrics.qualified_read_count + ?", qualifiedReads),
			"quick_bounce_count":   gorm.Expr("recommendation_daily_metrics.quick_bounce_count + ?", quickBounces),
			"not_interested_count": gorm.Expr("recommendation_daily_metrics.not_interested_count + ?", notInterested),
			"feed_dwell_count":     gorm.Expr("recommendation_daily_metrics.feed_dwell_count + ?", feedDwellCount),
			"feed_visible_time_ms": gorm.Expr("recommendation_daily_metrics.feed_visible_time_ms + ?", feedVisibleTimeMS),
			"updated_at":           time.Now().UTC(),
		}),
	}).Create(&metric).Error
}

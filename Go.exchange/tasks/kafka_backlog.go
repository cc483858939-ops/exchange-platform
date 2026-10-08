package tasks

import (
	"context"
	"log"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/metrics"
)

const kafkaBacklogMaxAge = 30 * time.Second

func isKafkaPipeline(name string) bool {
	switch name {
	case PipelineUserBehaviorProjection, PipelineNotificationProjection, PipelineRecommendationMetrics, PipelineLikeSnapshotProjection, PipelinePostEmbedding:
		return true
	default:
		return false
	}
}

func refreshKafkaCommittedBacklogs(ctx context.Context) {
	if config.AppConfig == nil {
		return
	}
	cfg := config.AppConfig.Kafka
	groups := []struct{ pipeline, topic, group string }{
		{PipelineUserBehaviorProjection, cfg.UserBehaviorTopic, cfg.UserBehaviorGroupID},
		{PipelineLikeSnapshotProjection, cfg.LikeSnapshotTopic, cfg.LikeSnapshotGroupID},
		{PipelineRecommendationMetrics, cfg.RecommendationEventsTopic, cfg.RecommendationMetricsGroupID},
	}
	if notificationConsumerConfigured() {
		groups = append(groups, struct{ pipeline, topic, group string }{PipelineNotificationProjection, cfg.ActivityEventsTopic, cfg.NotificationGroupID})
	}
	if config.AppConfig.Embedding.Enabled {
		groups = append(groups, struct{ pipeline, topic, group string }{PipelinePostEmbedding, cfg.PostEmbeddingTopic, cfg.PostEmbeddingGroupID})
	}
	for _, entry := range groups {
		lag, err := eventing.KafkaCommittedLag(ctx, cfg, entry.topic, entry.group)
		now := time.Now().UTC()
		pipelineKafkaBacklogSample(entry.pipeline, lag, now, err == nil)
		metrics.RecordKafkaCommittedLagSample(entry.group, entry.topic, lag, now, err == nil)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("[Metrics] committed Kafka lag group=%s topic=%s: %v", entry.group, entry.topic, err)
			}
		} else if entry.pipeline == PipelineNotificationProjection {
			metrics.SetNotificationConsumerLag(float64(lag))
		}
	}
}

func pipelineKafkaBacklogSample(name string, backlog int64, now time.Time, valid bool) {
	workerPipelines.Lock()
	state, found := workerPipelines.states[name]
	if found {
		state.backlogSampleValid = valid && backlog >= 0
		if state.backlogSampleValid {
			setPipelineBacklog(&state, backlog, now)
			state.backlogSampleAt = now
		}
		workerPipelines.states[name] = state
	}
	workerPipelines.Unlock()
}

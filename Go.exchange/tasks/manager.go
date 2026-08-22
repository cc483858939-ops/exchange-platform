package tasks

import (
	"context"
	"sync"
)

func StartAll(ctx context.Context, wg *sync.WaitGroup) {
	startExchangeRateRefresh(ctx, wg)
	startArticleEmbeddingConsumer(ctx, wg)
	startUserBehaviorProjectionConsumer(ctx, wg)
	startNotificationProjectionConsumer(ctx, wg)
	startRecommendationMetricsConsumer(ctx, wg)
	startRecommendationProfileMaterializer(ctx, wg)
	startLikeStateRelay(ctx, wg)
	startLikeSnapshotRelay(ctx, wg)
	startLikeSnapshotProjectionConsumer(ctx, wg)
	startWorkerReadinessProbe(ctx, wg)
	startPipelineMetrics(ctx, wg)
	startOutboxRetention(ctx, wg)
	startRecommendationTraceCleanup(ctx, wg)
}

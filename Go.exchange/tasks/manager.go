package tasks

import (
	"context"
	"sync"
)

func StartAll(ctx context.Context, wg *sync.WaitGroup) {
	startExchangeRateRefresh(ctx, wg)
	startOutboxRelay(ctx, wg)
	startArticleAnalysisWorkers(ctx, wg)
	startUserBehaviorProjectionConsumer(ctx, wg)
	startRecommendationMetricsConsumer(ctx, wg)
	startLikeStateRelay(ctx, wg)
	startLikeSnapshotRelay(ctx, wg)
	startLikeSnapshotProjectionConsumer(ctx, wg)
	startWorkerReadinessProbe(ctx, wg)
	startPipelineMetrics(ctx, wg)
}

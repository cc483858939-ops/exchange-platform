package tasks

import (
	"context"
	"log"
	"sync"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/metrics"
)

const (
	StaticWorkerCount = 10
	MaxDynamicWorker  = 30
	BacklogThreshold  = 500
	targetSpawn       = 5
)

var sem = make(chan struct{}, MaxDynamicWorker)

var dirtyBacklogCount = func() (int64, error) {
	return global.RedisDB.SCard(consts.ArticleDirtySetKey).Result()
}

func init() {
	metrics.SetDynamicWorkersFunc(func() float64 {
		return float64(len(sem))
	})
}

func recoverOrphanedData() {
	count, err := global.RedisDB.SCard(consts.ArticleProcessingSetKey).Result()
	if err != nil {
		log.Printf("[Task] [Recover] failed to read processing set: %v", err)
		return
	}
	if count == 0 {
		return
	}

	err = global.RedisDB.SUnionStore(
		consts.ArticleDirtySetKey,
		consts.ArticleDirtySetKey,
		consts.ArticleProcessingSetKey,
	).Err()
	if err != nil {
		log.Printf("[Task] [Recover] failed to merge processing set back to dirty set: %v", err)
		return
	}

	global.RedisDB.Del(consts.ArticleProcessingSetKey)
	log.Printf("[Task] [Recover] moved %d orphaned ids back to dirty set", count)
}

func StartAll(ctx context.Context, wg *sync.WaitGroup) {
	recoverOrphanedData()
	startArticleAnalysisWorkers(ctx, wg)

	for i := 0; i < StaticWorkerCount; i++ {
		wg.Add(1)
		go staticLoop(ctx, wg)
	}
	log.Printf("[Task] started %d static workers", StaticWorkerCount)

	wg.Add(1)
	go func() {
		defer wg.Done()

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("[Task] scheduler stopped")
				return
			case <-ticker.C:
				select {
				case sem <- struct{}{}:
					<-sem
					checkAndScale(ctx, wg)
				default:
					log.Printf("[Task] dynamic workers already at capacity (%d)", cap(sem))
				}
			}
		}
	}()
}

func checkAndScale(ctx context.Context, wg *sync.WaitGroup) {
	backlogCount, err := dirtyBacklogCount()
	if err != nil {
		log.Printf("[Task] failed to read dirty backlog: %v", err)
		return
	}

	metrics.SetDirtyBacklog(float64(backlogCount))
	if backlogCount < BacklogThreshold {
		return
	}

	spawnCount := 0
Loop:
	for i := 0; i < targetSpawn; i++ {
		select {
		case sem <- struct{}{}:
			wg.Add(1)
			go dynamicLoop(ctx, wg)
			spawnCount++
		default:
			break Loop
		}
	}

	if spawnCount > 0 {
		log.Printf("[Task] backlog=%d, spawned %d dynamic workers", backlogCount, spawnCount)
	}
}

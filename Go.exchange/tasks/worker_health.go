package tasks

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
)

var workerReady atomic.Bool
var articleAnalysisConsumers atomic.Int32
var userBehaviorConsumers atomic.Int32
var likeSnapshotConsumers atomic.Int32
var outboxRelayRunning atomic.Bool
var likeEventRelayRunning atomic.Bool
var likeBehaviorRelayWorkers atomic.Int32
var likeSnapshotRelayRunning atomic.Bool

func WorkerReady() bool {
	return workerReady.Load() && outboxRelayRunning.Load() && articleAnalysisConsumers.Load() > 0 && userBehaviorConsumers.Load() > 0 && likeSnapshotConsumers.Load() > 0 && likeBehaviorRelayWorkers.Load() > 0 && likeSnapshotRelayRunning.Load()
}
func refreshWorkerReadiness(ctx context.Context) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	db, err := global.Db.DB()
	if err != nil {
		return err
	}
	if err = db.PingContext(ctx); err != nil {
		return err
	}
	if global.RedisDB == nil {
		return errors.New("redis is not initialized")
	}
	if err = global.RedisDB.Ping().Err(); err != nil {
		return err
	}
	return eventing.KafkaReachable(ctx, config.AppConfig.Kafka)
}
func startWorkerReadinessProbe(ctx context.Context, wg interface {
	Add(int)
	Done()
}) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			probe, cancel := context.WithTimeout(ctx, 2*time.Second)
			workerReady.Store(refreshWorkerReadiness(probe) == nil)
			cancel()
			select {
			case <-ctx.Done():
				workerReady.Store(false)
				return
			case <-ticker.C:
			}
		}
	}()
}

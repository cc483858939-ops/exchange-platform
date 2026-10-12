package tasks

import (
	"context"
	"log"
	"sync"
	"time"

	"Go.exchange/global"
	"Go.exchange/likes"
	"Go.exchange/postlikeinit"
	"gorm.io/gorm"
)

func startPostLikeInit(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			passCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := runPostLikeInitPass(passCtx, global.WorkerDb, likes.NewStore(global.RedisDB), time.Now().UTC())
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("[PostLikeInit] process durable initialization tasks: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func runPostLikeInitPass(ctx context.Context, db *gorm.DB, store *likes.Store, now time.Time) error {
	return postlikeinit.RunPass(ctx, db, store, now)
}

package tasks

import (
	"context"
	"log"
	"sync"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
)

const (
	StaticWorkerCount = 10
	MaxDynamicWorker  = 30
	BacklogThreshold  = 500
	targetSpawn       = 5
)

var sem = make(chan struct{}, MaxDynamicWorker)

// recoverOrphanedData 崩溃恢复策略
// 在启动 Worker 前，如果 ProcessingSet 中有残留数据（说明上次程序崩溃挂掉导致未消费/落库完），
// 将这些因为崩溃遗留在 Processing 集合的孤立数据回退到 Dirty 集合重新处理。
func recoverOrphanedData() {
	count, err := global.RedisDB.SCard(consts.ArticleProcessingSetKey).Result()
	if err != nil {
		log.Printf("[Task] [Recover] 读取 ProcessingSet 失败: %v", err)
		return
	}
	if count > 0 {
		// 使用 SUnionStore 将 ProcessingSet 合并回 DirtySet 并覆盖 DirtySet
		err = global.RedisDB.SUnionStore(consts.ArticleDirtySetKey, consts.ArticleDirtySetKey, consts.ArticleProcessingSetKey).Err()
		if err == nil {
			global.RedisDB.Del(consts.ArticleProcessingSetKey)
			log.Printf("[Task] [Recover] 崩溃恢复：成功将 %d 个孤立的 ID 退回到 DirtySet", count)
		} else {
			log.Printf("[Task] [Recover] 崩溃恢复合并失败: %v", err)
		}
	}
}

func StartAll(ctx context.Context, wg *sync.WaitGroup) {
	// 启动 Worker 前执行崩溃恢复
	recoverOrphanedData()

	//先开启固定的协程
	for i := 0; i < StaticWorkerCount; i++ {
		wg.Add(1)
		go staticLoop(ctx, wg)
	}
	log.Printf("[Task] 已启动 %d 个常驻同步协程", StaticWorkerCount)
	wg.Add(1)
	go func() {
		defer wg.Done()

		// 如果 Redis 里积压数据很多，每 2 秒就会新启动一个协程
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Println("[Task] 调度器收到停止信号，停止分发新任务")
				return
			case <-ticker.C:
				// 尝试获取一个信号量，用以判断是否已达动态扩容上限
				select {
				case sem <- struct{}{}:
					// 成功获取一个令牌，立即释放，因为我们只是检查容量
					// 并且我们认为当前容量有空闲，可以尝试进行扩容检查
					<-sem

					checkAndScale(ctx, wg) // 积压检查和扩容
				default:
					// 令牌发完了，说明当前并发已达上限
					log.Printf("[Task] 动态协程并发达到上限 (%d)，跳过本次扩容检查...", cap(sem))
				}
			}
		}
	}()
}
func checkAndScale(ctx context.Context, wg *sync.WaitGroup) {
	//先检查redis里面积压了多少数据达到了才开启任务
	backlogCount, err := global.RedisDB.SCard(consts.ArticleDirtySetKey).Result()
	if err != nil {
		log.Printf("获取积压量失败")
		return
	}
	if backlogCount < BacklogThreshold {
		return
	}
	//此时积压量多开辟新任务，每次开启五个新的.这里还真要看看令牌发完没，发完了就退没发完才继续
	spawnCount := 0
Loop:
	for i := 0; i < targetSpawn; i++ {
		select {
		case sem <- struct{}{}:
			wg.Add(1)
			go dynamicLoop(ctx, wg)
			spawnCount++
		default:
			// 令牌发完了，跳出整个 Loop 循环
			break Loop
		}
	}
	if spawnCount > 0 {
		log.Printf("[Task] 积压警告: %d, 动态启动了 %d 个 Worker 支援", backlogCount, spawnCount)
	}
}

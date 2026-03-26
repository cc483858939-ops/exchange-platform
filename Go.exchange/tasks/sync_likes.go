package tasks

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// --- 可 mock 的函数变量，方便单元测试 ---

var batchUpsert = func(articles []models.Article) error {
	return global.Db.
		Session(&gorm.Session{SkipDefaultTransaction: true}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"like_count"}),
		}).
		Create(&articles).Error
}

var singleUpsert = func(article models.Article) error {
	return global.Db.
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"like_count"}),
		}).
		Create(&article).Error
}

var incrRetryCount = func(idStr string) (int64, error) {
	return global.RedisDB.HIncrBy(consts.ArticleLikeRetryCountKey, idStr, 1).Result()
}

var moveToDeadLetter = func(ids ...interface{}) error {
	pipe := global.RedisDB.Pipeline()
	pipe.SAdd(consts.ArticleLikeDeadLetterKey, ids...)
	pipe.SRem(consts.ArticleProcessingSetKey, ids...)
	_, err := pipe.Exec()
	return err
}

var rollbackToRetry = func(ids ...interface{}) error {
	pipe := global.RedisDB.Pipeline()
	pipe.SAdd(consts.ArticleDirtySetKey, ids...)
	pipe.SRem(consts.ArticleProcessingSetKey, ids...)
	_, err := pipe.Exec()
	return err
}

var ackSuccess = func(ids ...interface{}) error {
	return global.RedisDB.SRem(consts.ArticleProcessingSetKey, ids...).Err()
}

// ---

func staticLoop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		hasData := fetchAndProcessBatch()
		if !hasData {
			time.Sleep(1 * time.Second)
		}
	}
}

func dynamicLoop(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() {
		<-sem
	}()
	timer := time.NewTimer(0)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	cout := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		hasData := fetchAndProcessBatch()

		if hasData {
			cout = 0
		} else {
			cout++
			if cout >= 3 {
				return
			}
			sleepDuration := 200*time.Millisecond + time.Duration(rand.Intn(200))*time.Millisecond
			timer.Reset(sleepDuration)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}
}

func fetchAndProcessBatch() bool {
	val, err := global.RedisDB.Eval(
		consts.FetchSafeBatchScript,
		[]string{consts.ArticleDirtySetKey, consts.ArticleProcessingSetKey},
		100,
		consts.ArticleLikeKey,
	).Result()

	if err != nil {
		if err != redis.Nil {
			log.Printf("[Sync] Lua Error: %v", err)
		}
		return false
	}

	resultList, ok := val.([]interface{})
	if !ok || len(resultList) == 0 {
		return false
	}

	processBatchData(resultList)
	return true
}

func processBatchData(data []interface{}) {
	type entry struct {
		idStr   string
		article models.Article
	}

	var entries []entry
	for i := 0; i < len(data); i += 2 {
		rawID := data[i]
		rawVal := data[i+1]
		if rawID == nil || rawVal == nil {
			continue
		}

		idStr := fmt.Sprintf("%v", rawID)
		valStr := fmt.Sprintf("%v", rawVal)
		idInt64, err1 := strconv.ParseInt(idStr, 10, 64)
		likes, err2 := strconv.ParseInt(valStr, 10, 64)
		if err1 != nil || err2 != nil {
			log.Printf("[Sync] 解析 id 或点赞数失败: ID=%v, Val=%v", rawID, rawVal)
			continue
		}
		entries = append(entries, entry{
			idStr: idStr,
			article: models.Article{
				Model:     gorm.Model{ID: uint(idInt64)},
				LikeCount: likes,
			},
		})
	}

	if len(entries) == 0 {
		return
	}

	// 构建批量写入列表
	articles := make([]models.Article, 0, len(entries))
	for _, e := range entries {
		articles = append(articles, e.article)
	}

	// Step 1: 尝试批量 upsert（已关闭默认事务，避免全批回滚）
	if err := batchUpsert(articles); err == nil {
		// 全批成功，ACK
		ids := make([]interface{}, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.idStr)
		}
		if ackErr := ackSuccess(ids...); ackErr != nil {
			log.Printf("[Sync] ACK 失败: %v", ackErr)
		}
		return
	}

	// Step 2: 批量写入失败，降级为逐条 upsert，精确识别问题数据
	log.Printf("[Sync] 批量 upsert 失败，降级为逐条处理，共 %d 条", len(entries))
	for _, e := range entries {
		if err := singleUpsert(e.article); err == nil {
			// 单条成功
			if ackErr := ackSuccess(e.idStr); ackErr != nil {
				log.Printf("[Sync] 单条 ACK 失败: id=%s, %v", e.idStr, ackErr)
			}
			continue
		}

		// Step 3: 单条失败，记录重试次数
		retryCount, incrErr := incrRetryCount(e.idStr)
		if incrErr != nil {
			log.Printf("[Sync] 记录重试次数失败: id=%s, %v", e.idStr, incrErr)
			// 保守策略：回退到 dirty set 等待重试
			_ = rollbackToRetry(e.idStr)
			continue
		}

		if retryCount >= consts.MaxRetryCount {
			// Step 4: 超过最大重试次数，进死信，不再重试
			log.Printf("[Sync] ⚠️  死信告警: article id=%s 已失败 %d 次，移入死信集合，请人工排查", e.idStr, retryCount)
			if dlErr := moveToDeadLetter(e.idStr); dlErr != nil {
				log.Printf("[Sync] 移入死信失败: id=%s, %v", e.idStr, dlErr)
			}
		} else {
			// 还有重试机会，回滚到 dirty set
			log.Printf("[Sync] article id=%s 落库失败，第 %d 次，回队列重试", e.idStr, retryCount)
			if rbErr := rollbackToRetry(e.idStr); rbErr != nil {
				log.Printf("[Sync] 回滚到 dirty set 失败: id=%s, %v", e.idStr, rbErr)
			}
		}
	}
}

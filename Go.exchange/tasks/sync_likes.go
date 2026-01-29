package tasks

import (
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"
	"context"
	"math/rand"
	"time"

	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func staticLoop(ctx context.Context, wg *sync.WaitGroup) {
	//退出的时候记得把对应的协程wg.done,如果有数据就处理如果没有数据就sleep
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		hasData := fetchAndProcessBatch()
		if !hasData {
			time.Sleep(1 * time.Second) //没数据就休眠
		}
	}
}
func dynamicLoop(ctx context.Context, wg *sync.WaitGroup) {
	//对于这种临时协程那么就饥饿释放和信号释放，而且要归还信号量
	defer wg.Done()
	defer func() {
		<-sem
	}()
	timer := time.NewTimer(0) //不用time.after免得GC炸掉
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	//如果连续三次都出现了没抢到数据那么说明这个时候流量已经处理得差不多了就可以将他销毁掉了
	cout := 0
	for {
		// 每次循环开始先检查一下
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
			timer.Reset(sleepDuration) //设置为0.2秒+随机时间免得cpu空转和redis的惊群效应，讲道理也能防止同一时间的销毁压力
			select {
			case <-ctx.Done():
				//如果在等待期间收到退出信号，直接返回，用死等
				return
			case <-timer.C:
				// 等待 500ms 什么都不做，继续下一次循环
			}

		}
	}
}
func fetchAndProcessBatch() bool {
	// Lua 脚本原子获取
	val, err := global.RedisDB.Eval(
		consts.FetchSafeBatchScript,
		[]string{consts.ArticleDirtySetKey, consts.ArticleProcessingSetKey},
		100, // 每次取100条
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

	// 具体的业务处理逻辑
	processBatchData(resultList)
	return true
}
func processBatchData(data []interface{}) {

	var article []models.Article
	var successIDs []interface{}
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
			log.Printf("解析id或者点赞失败: ID=%v, Val=%v", rawID, rawVal)
			continue
		}
		article = append(article, models.Article{
			Model:     gorm.Model{ID: uint(idInt64)},
			LikeCount: likes,
		})
		successIDs = append(successIDs, rawID)
		// 写入 MySQL

	}
	err := global.Db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"like_count"}),
	}).Create(&article).Error
	if err != nil {
		log.Printf("[Sync] Batch Update Error: %v", err)
	} else {
		if len(successIDs) > 0 {
			global.RedisDB.SRem(consts.ArticleProcessingSetKey, successIDs...)
		}
	}
	// 从 Processing Set 中移除 (ACK)

}

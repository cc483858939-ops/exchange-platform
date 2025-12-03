package tasks

import (
	"aceld/consts"
	"aceld/global"
	"errors"

	// 引入常量包
	"aceld/models"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/go-redis/redis/v7"
)

func SyncLikesToMySQL() {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("[Sync] 定时任务发生 Panic: %v", err)
			// 可以在这里选择重启协程: go SyncLikesToMySQL()
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		syncLogic()
	}
}
func syncLogic() {
	ids, err := global.RedisDB.SPopN(consts.ArticleDirtySetKey, 100).Result()
	if err != nil {
		log.Println("[Sync] 获取 dirty set 失败:", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	log.Printf("[Sync] 检测到 %d 篇文章点赞数变化，开始同步...", len(ids))
	for _, id := range ids {
		likeKey := fmt.Sprintf(consts.ArticleLikeKey, id)
		valStr, err := global.RedisDB.Get(likeKey).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				log.Printf("[Sync] 异常：文章 %s 缓存丢失，无法同步", id)
			} else {
				log.Printf("[Sync] 网络故障，将 %s 放回集合重试", id)
				global.RedisDB.SAdd(consts.ArticleDirtySetKey, id)
			}

			continue
		}
		likes, _ := strconv.ParseInt(valStr, 10, 64)
		err = global.Db.Model(&models.Article{}).Where("id = ?", id).Update("like_count", likes).Error

		if err != nil {
			log.Printf("[Sync] MySQL 更新失败 %s: %v", id, err)
			global.RedisDB.SAdd(consts.ArticleDirtySetKey, id)
		}
	}
}

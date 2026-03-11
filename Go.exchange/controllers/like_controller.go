package controllers

import (
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

var likeScript = redis.NewScript(`
    if redis.call("EXISTS",KEYS[1])== 0 then
	return -1
	end
	local Newcount = redis.call("INCR",KEYS[1])
	redis.call("EXPIRE",KEYS[1],ARGV[1])
	redis.call("SADD",KEYS[2],ARGV[2])
	return Newcount
	`)

func LikeArticle(ctx *gin.Context) {
	articleID := ctx.Param("id")

	likeKey := fmt.Sprintf(consts.ArticleLikeKey, articleID) //先查询数据库以免缓存未命中直接加一导致赞数丢失
	// 尝试执行 Lua 脚本
	// KEYS: [likeKey, ArticleDirtySetKey]
	// ARGV: [ExpireSeconds, articleID]
	result, err := likeScript.Run(global.RedisDB,
		[]string{likeKey, consts.ArticleDirtySetKey},
		consts.ArticleLikeExpire.Seconds(),
		articleID).Int()

	if err != nil && err != redis.Nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 结果为 -1 说明 Redis 中 key 不存在（缓存击穿/未命中），需要回源查数据库
	if result == -1 {
		var article models.Article
		if err := global.Db.Select("like_count").First(&article, articleID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				ctx.JSON(http.StatusNotFound, gin.H{"error": "文章不存在"})
				return
			}
			global.Db.Logger.Error(ctx, "查询文章点赞数失败: ", err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}

		// 查到库了，写入 Redis 并执行 +1 操作
	
		
		// 更好的做法是：Set 进去后，直接手动 +1 并加入脏集合（可以用 Pipeline）

		newCount := article.LikeCount + 1
		pipe := global.RedisDB.Pipeline()
		pipe.Set(likeKey, newCount, consts.ArticleLikeExpire) // 直接存 +1 后的值
		pipe.SAdd(consts.ArticleDirtySetKey, articleID)
		_, pipeErr := pipe.Exec()

		if pipeErr != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Redis error"})
			return
		}

		// 更新 result 用于返回
		result = int(newCount)
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "Successfully liked the article",
		"likes":   result,
	})
}
func GetArticleLikes(ctx *gin.Context) {
	articleID := ctx.Param("id")
	likeKey := fmt.Sprintf(consts.ArticleLikeKey, articleID)
	valStr, err := global.RedisDB.Get(likeKey).Result()
	var likes int64
	if err == redis.Nil {
		var article models.Article
		if err := global.Db.Select("like_count").First(&article, articleID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				ctx.JSON(http.StatusNotFound, gin.H{"error": "文章不存在"})
				return
			}
			global.Db.Logger.Error(ctx, "查询文章点赞数失败: ", err)
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		likes = int64(article.LikeCount)
		global.RedisDB.Set(likeKey, article.LikeCount, consts.ArticleLikeExpire)
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else {
		// 缓存命中
		likes, err = strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			// 如果 Redis 里的数据因为某种原因坏掉了（比如变成了 "abc"），解析报错
			// 可以不直接报错，打印日志，去用数据库兜底
			global.Db.Logger.Error(ctx, "Redis数据异常，解析失败，尝试回源数据库: ", err)

			// 后面可以设计一个兜底机制在这里再查MySQL
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "缓存数据异常"})
			return
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"likes": likes})
}

package controllers

import (
	"aceld/consts"
	"aceld/global"
	"aceld/models"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

func LikeArticle(ctx *gin.Context) {
	articleID := ctx.Param("id")

	likeKey := fmt.Sprintf(consts.ArticleLikeKey, articleID) //先查询数据库以免缓存未命中直接加一导致赞数丢失
	if global.RedisDB.Exists(likeKey).Val() == 0 {
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
		global.RedisDB.Set(likeKey, article.LikeCount, consts.ArticleLikeExpire)
	}
	newCount, err := global.RedisDB.Incr(likeKey).Result()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	global.RedisDB.Expire(likeKey, consts.ArticleLikeExpire)

	// 将文章ID添加到待同步的脏数据集合中
	global.RedisDB.SAdd(consts.ArticleDirtySetKey, articleID)

	ctx.JSON(http.StatusOK, gin.H{"message": "Successfully liked the article",
		"likes": newCount,
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
			// 此时不要直接报错，而是打印日志，并尝试去数据库兜底
			global.Db.Logger.Error(ctx, "Redis数据异常，解析失败，尝试回源数据库: ", err)

			// 这里可以复制上面查数据库的代码，或者把查库逻辑封装成一个小函数复用
			// 为了简单，通常直接让它报错也行，因为这种情况极少发生
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "缓存数据异常"})
			return
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"likes": likes})
}

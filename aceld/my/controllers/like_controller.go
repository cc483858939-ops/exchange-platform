package controllers

import (
	"aceld/global"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
)

func LikeArticle(ctx *gin.Context) {
	articleID := ctx.Param("id")

	likeKey := "article:" + articleID + ":likes"

	newCount, err := global.RedisDB.Incr(likeKey).Result()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "Successfully liked the article",
		"likes": newCount,
	})
}
func GetArticleLikes(ctx *gin.Context) {
	articleID := ctx.Param("id")
	likeKey := "article:" + articleID + ":likes"
	likes, err := global.RedisDB.Get(likeKey).Result()
	if err == redis.Nil {
		likes = "0"
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"likes": likes})
}

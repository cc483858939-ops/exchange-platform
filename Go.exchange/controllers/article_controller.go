package controllers

import (
	"Go.exchange/global"
	"Go.exchange/models"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
)

var cacheKey = "articles"

func CreateArticle(ctx *gin.Context) {
	var article models.Article
	if err := ctx.ShouldBind(&article); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := global.Db.Create(&article).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := global.RedisDB.Del(cacheKey).Err(); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, article)
}

func GetArticle(ctx *gin.Context) {

	cachedDate, err := global.RedisDB.Get(cacheKey).Result()
	if err == redis.Nil {
		var articles []models.Article
		if err := global.Db.Select("id,title,preview,expired_at,create_at,update_at,delete_at").Where("expired_at > ? OR expired_at IS null", time.Now()).Order("created_at desc").Find(&articles).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			} else {
				ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}
		articleJSON, err := json.Marshal(articles)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := global.RedisDB.Set(cacheKey, articleJSON, 10*time.Minute).Err(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, articles)
	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else {
		var articles []models.Article
		if err := json.Unmarshal([]byte(cachedDate), &articles); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, articles)
	}

}
func GetArticleByID(ctx *gin.Context) {
	id := ctx.Param("id")
	detailKey := "article:detail" + id
	cachedData, err := global.RedisDB.Get(detailKey).Result()
	if err == redis.Nil {
		var article models.Article
		if err := global.Db.Where("id = ? AND (expired_at > ? OR expired_at IS NULL)", id, time.Now()).First(&article).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			} else {
				ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		articleJSON, err := json.Marshal(article)
		if err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := global.RedisDB.Set(detailKey, articleJSON, 10*time.Minute).Err(); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, article)

	} else if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	} else {
		var article models.Article
		if err := json.Unmarshal([]byte(cachedData), &article); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ctx.JSON(http.StatusOK, article)
	}
}

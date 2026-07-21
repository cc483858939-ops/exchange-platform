package controllers

import (
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var likeScript = redis.NewScript(`
	if redis.call("EXISTS",KEYS[1]) == 0 then
		redis.call("SET",KEYS[1],ARGV[3])
	end
	local Current = tonumber(redis.call("GET",KEYS[1]) or "0")
	local Delta = tonumber(ARGV[1])
	local Newcount = Current + Delta
	if Newcount < 0 then
		Newcount = 0
	end
	redis.call("SET",KEYS[1],Newcount)
	redis.call("EXPIRE",KEYS[1],ARGV[2])
	redis.call("SADD",KEYS[2],ARGV[4])
	return Newcount
	`)

type articleLikeMutationResult struct {
	Likes            int64
	Liked            bool
	ChangedToLiked   bool
	ChangedToUnliked bool
}

type articleLikeStateResult struct {
	Likes int64
	Liked bool
}

var setArticleLikedState = setArticleLikedStateWithDB
var loadArticleLikeState = loadArticleLikeStateFromDB
var applyArticleLikeDelta = applyArticleLikeDeltaWithRedis
var loadArticleLikeBaseline = loadArticleLikeBaselineFromDB
var insertArticleLikeReaction = insertArticleLikeReactionWithDB
var deleteArticleLikeReaction = deleteArticleLikeReactionWithDB
var loadArticleLikeCount = getArticleLikeCount

func LikeArticle(ctx *gin.Context) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}

	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Missing user"})
		return
	}

	result, err := setArticleLikedState(userID, articleID, true) //改变点赞状态，并增加点赞数
	if err != nil {
		writeArticleLikeError(ctx, err)
		return
	}

	if result.ChangedToLiked {
		recordArticleBehaviorFromContext(ctx, articleID, ArticleBehaviorActionLike) //记录用户点赞行为
	}
	ctx.JSON(http.StatusOK, gin.H{
		"message": "Successfully liked the article",
		"likes":   result.Likes,
		"liked":   result.Liked,
	})
}

func UnlikeArticle(ctx *gin.Context) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}

	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Missing user"})
		return
	}

	result, err := setArticleLikedState(userID, articleID, false) //改变点赞状态，并减少点赞数
	if err != nil {
		writeArticleLikeError(ctx, err)
		return
	}

	if result.ChangedToUnliked {
		if err := archiveArticleBehavior(userID, articleID, ArticleBehaviorActionLike); err != nil { //记录用户取消点赞行为
			articleBehaviorLogError(ctx, "failed to archive article like behavior", err)
		}
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message": "Successfully unliked the article",
		"likes":   result.Likes,
		"liked":   result.Liked,
	})
}

func GetArticleLikes(ctx *gin.Context) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}

	userID, _ := userIDFromContext(ctx)
	result, err := loadArticleLikeState(userID, articleID)
	if err != nil {
		writeArticleLikeError(ctx, err)
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"likes": result.Likes, "liked": result.Liked})
} //获取点赞数

func articleIDFromContext(ctx *gin.Context) (uint, bool) {
	idUint64, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || idUint64 == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article id"})
		return 0, false
	}
	return uint(idUint64), true
}

func writeArticleLikeError(ctx *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "文章不存在"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func setArticleLikedStateWithDB(userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
	var result articleLikeMutationResult
	if global.Db == nil {
		return result, errors.New("database is not initialized")
	}

	err := global.Db.Transaction(func(tx *gorm.DB) error {
		nextResult, err := setArticleLikedStateInTx(tx, userID, articleID, liked)
		result = nextResult
		return err
	})
	return result, err
}

func setArticleLikedStateInTx(tx *gorm.DB, userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
	var result articleLikeMutationResult

	article, err := loadArticleLikeBaseline(tx, articleID)//这里读点赞数，如果redis有缓存的话是不会用的只作baseline
	if err != nil {
		return result, err
	}

	changed := false
	var delta int64

	if liked {                                                         //这里的liked是传进来的用户想要达到的状态
		changed, err = insertArticleLikeReaction(tx, userID, articleID)
		if err != nil {
			return result, err
		}
		if changed {
			delta = 1
		}
	} else {
		changed, err = deleteArticleLikeReaction(tx, userID, articleID)
		if err != nil {
			return result, err
		}
		if changed {
			delta = -1
		}
	}

	if delta != 0 {
		nextLikes, err := applyArticleLikeDelta(articleID, delta, article.LikeCount)
		if err != nil {
			return result, err
		}
		result.Likes = nextLikes
	} else {
		currentLikes, err := loadArticleLikeCount(articleID)
		if err != nil {
			return result, err
		}
		result.Likes = currentLikes
	}

	result.Liked = liked
	result.ChangedToLiked = liked && changed
	result.ChangedToUnliked = !liked && changed
	return result, nil
}

func loadArticleLikeBaselineFromDB(tx *gorm.DB, articleID uint) (models.Article, error) {
	var article models.Article
	err := tx.
		Select("id", "like_count").
		First(&article, articleID).Error
	return article, err
}

func insertArticleLikeReactionWithDB(tx *gorm.DB, userID uint, articleID uint) (bool, error) {
	reaction := models.ArticleReaction{
		UserID:    userID,
		ArticleID: articleID,
		Reaction:  models.ArticleReactionLike,
	}

	result := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "article_id"},
		},
		DoNothing: true,
	}).Create(&reaction)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func deleteArticleLikeReactionWithDB(tx *gorm.DB, userID uint, articleID uint) (bool, error) {
	result := tx.
		Where("user_id = ? AND article_id = ?", userID, articleID).
		Delete(&models.ArticleReaction{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func loadArticleLikeStateFromDB(userID uint, articleID uint) (articleLikeStateResult, error) {
	likes, err := getArticleLikeCount(articleID)
	if err != nil {
		return articleLikeStateResult{}, err
	}

	result := articleLikeStateResult{Likes: likes}
	if userID == 0 {
		return result, nil
	}

	var reaction models.ArticleReaction
	err = global.Db.
		Where("user_id = ? AND article_id = ? AND reaction = ?", userID, articleID, models.ArticleReactionLike).
		First(&reaction).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return result, nil
		}
		return result, err
	}
	result.Liked = true
	return result, nil
}

func getArticleLikeCount(articleID uint) (int64, error) {
	if global.Db == nil {
		return 0, errors.New("database is not initialized")
	}
	if global.RedisDB == nil {
		var article models.Article
		if err := global.Db.Select("like_count").First(&article, articleID).Error; err != nil {
			return 0, err
		}
		return int64(article.LikeCount), nil
	}

	articleIDStr := strconv.FormatUint(uint64(articleID), 10)
	likeKey := fmt.Sprintf(consts.ArticleLikeKey, articleIDStr)
	valStr, err := global.RedisDB.Get(likeKey).Result()
	var likes int64
	if err == redis.Nil {
		var article models.Article
		if err := global.Db.Select("like_count").First(&article, articleID).Error; err != nil {
			return 0, err
		}
		likes = int64(article.LikeCount)
		global.RedisDB.Set(likeKey, article.LikeCount, consts.ArticleLikeExpire)
	} else if err != nil {
		return 0, err
	} else {
		// 缓存命中
		likes, err = strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			// 如果 Redis 里的数据因为某种原因坏掉了（比如变成了 "abc"），解析报错
			// 可以不直接报错，打印日志，去用数据库兜底
			global.Db.Logger.Error(context.Background(), "Redis数据异常，解析失败，尝试回源数据库: ", err)

			return 0, errors.New("缓存数据异常")
		}
	}

	return likes, nil
}

func applyArticleLikeDeltaWithRedis(articleID uint, delta int64, baselineLikes int64) (int64, error) {
	if global.RedisDB == nil {
		return 0, errors.New("redis is not initialized")
	}
	if baselineLikes < 0 {
		baselineLikes = 0
	}

	articleIDStr := strconv.FormatUint(uint64(articleID), 10)
	likeKey := fmt.Sprintf(consts.ArticleLikeKey, articleIDStr) //先查询数据库以免缓存未命中直接加一导致赞数丢失
	result, err := likeScript.Run(global.RedisDB,
		[]string{likeKey, consts.ArticleDirtySetKey},
		delta,
		consts.ArticleLikeExpire.Seconds(),
		baselineLikes,
		articleIDStr).Int()
	if err != nil && err != redis.Nil {
		return 0, err
	}
	return int64(result), nil
}

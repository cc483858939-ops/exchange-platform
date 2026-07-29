package controllers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"Go.exchange/global"
	"Go.exchange/likes"

	"github.com/gin-gonic/gin"
)

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

var setArticleLikedState = setArticleLikedStateWithRedis
var loadArticleLikeState = loadArticleLikeStateFromRedis
var invalidateArticleLikeDetailCache = func(articleID uint) error {
	if global.RedisDB == nil {
		return nil
	}
	return InvalidateArticleDetailCacheByID(articleID)
}

func LikeArticle(ctx *gin.Context)   { mutateArticleLike(ctx, true) }
func UnlikeArticle(ctx *gin.Context) { mutateArticleLike(ctx, false) }

func mutateArticleLike(ctx *gin.Context, liked bool) {
	articleID, ok := articleIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Missing user"})
		return
	}
	result, err := setArticleLikedState(userID, articleID, liked)
	if err != nil {
		writeArticleLikeError(ctx, err)
		return
	}
	message := "Successfully unliked the article"
	if liked {
		message = "Successfully liked the article"
	}
	ctx.JSON(http.StatusOK, gin.H{"message": message, "likes": result.Likes, "liked": result.Liked})
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
}

func articleIDFromContext(ctx *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid article id"})
		return 0, false
	}
	return uint(id), true
}

func writeArticleLikeError(ctx *gin.Context, err error) {
	if errors.Is(err, likes.ErrNotReady) {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "article like state is not ready"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func setArticleLikedStateWithRedis(userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
	state, err := likes.NewStore(global.RedisDB).Mutate(context.Background(), userID, articleID, liked)
	if err != nil {
		return articleLikeMutationResult{}, err
	}
	if state.Changed {
		if err := invalidateArticleLikeDetailCache(articleID); err != nil {
			log.Printf("[Like] invalidate article %d cache: %v", articleID, err)
		}
	}
	return articleLikeMutationResult{
		Likes: state.Count, Liked: state.Liked,
		ChangedToLiked: liked && state.Changed, ChangedToUnliked: !liked && state.Changed,
	}, nil
}

func loadArticleLikeStateFromRedis(userID uint, articleID uint) (articleLikeStateResult, error) {
	state, err := likes.NewStore(global.RedisDB).Get(context.Background(), userID, articleID)
	return articleLikeStateResult{Likes: state.Count, Liked: state.Liked}, err
}

func getArticleLikeCount(articleID uint) (int64, error) {
	result, err := loadArticleLikeStateFromRedis(0, articleID)
	return result.Likes, err
}

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

type postLikeMutationResult struct {
	Likes            int64
	Liked            bool
	ChangedToLiked   bool
	ChangedToUnliked bool
}

type postLikeStateResult struct {
	Likes int64
	Liked bool
}

type postLikeStatesRequest struct {
	PostIDs []uint `json:"post_ids"`
}

type postLikeStateItem struct {
	PostID uint  `json:"post_id"`
	Likes  int64 `json:"likes"`
	Liked  bool  `json:"liked"`
}

type postLikeStatesResponse struct {
	Items              []postLikeStateItem `json:"items"`
	UnavailablePostIDs []uint              `json:"unavailable_post_ids"`
}

type postLikeStatesLoadResult struct {
	States      map[uint]postLikeStateResult
	Unavailable []uint
}

const maxPostLikeStateIDs = 100

var setPostLikedState = setPostLikedStateWithRedis
var loadPostLikeState = loadPostLikeStateFromRedis
var loadPostLikeStates = loadPostLikeStatesFromRedis
var invalidatePostLikeDetailCache = func(postID uint) error {
	if global.RedisDB == nil {
		return nil
	}
	return InvalidatePostDetailCacheByID(postID)
}

func LikePost(ctx *gin.Context)   { mutatePostLike(ctx, true) }
func UnlikePost(ctx *gin.Context) { mutatePostLike(ctx, false) }

func mutatePostLike(ctx *gin.Context, liked bool) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Missing user"})
		return
	}
	result, err := setPostLikedState(userID, postID, liked)
	if err != nil {
		writePostLikeError(ctx, err)
		return
	}
	message := "Successfully unliked the post"
	if liked {
		message = "Successfully liked the post"
	}
	ctx.JSON(http.StatusOK, gin.H{"message": message, "likes": result.Likes, "liked": result.Liked})
}

func GetPostLikes(ctx *gin.Context) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	userID, _ := userIDFromContext(ctx)
	result, err := loadPostLikeState(userID, postID)
	if err != nil {
		writePostLikeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"likes": result.Likes, "liked": result.Liked})
}

func GetPostLikeStates(ctx *gin.Context) {
	var request postLikeStatesRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post_ids"})
		return
	}
	if len(request.PostIDs) == 0 || len(request.PostIDs) > maxPostLikeStateIDs {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "post_ids must contain between 1 and 100 ids"})
		return
	}

	uniqueIDs := make([]uint, 0, len(request.PostIDs))
	seen := make(map[uint]struct{}, len(request.PostIDs))
	for _, postID := range request.PostIDs {
		if postID == 0 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "post_ids must contain positive ids"})
			return
		}
		if _, exists := seen[postID]; exists {
			continue
		}
		seen[postID] = struct{}{}
		uniqueIDs = append(uniqueIDs, postID)
	}

	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Missing user"})
		return
	}
	result, err := loadPostLikeStates(userID, uniqueIDs)
	if err != nil {
		writePostLikeError(ctx, err)
		return
	}

	unavailable := make(map[uint]struct{}, len(result.Unavailable))
	for _, postID := range result.Unavailable {
		unavailable[postID] = struct{}{}
	}
	response := postLikeStatesResponse{
		Items:              make([]postLikeStateItem, 0, len(result.States)),
		UnavailablePostIDs: make([]uint, 0, len(result.Unavailable)),
	}
	for _, postID := range uniqueIDs {
		if state, ready := result.States[postID]; ready {
			response.Items = append(response.Items, postLikeStateItem{
				PostID: postID,
				Likes:  state.Likes,
				Liked:  state.Liked,
			})
			continue
		}
		if _, ready := unavailable[postID]; ready {
			response.UnavailablePostIDs = append(response.UnavailablePostIDs, postID)
			continue
		}
		response.UnavailablePostIDs = append(response.UnavailablePostIDs, postID)
	}
	ctx.JSON(http.StatusOK, response)
}
func postIDFromContext(ctx *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(ctx.Param("id"), 10, 64)
	if err != nil || id == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post id"})
		return 0, false
	}
	return uint(id), true
}

func writePostLikeError(ctx *gin.Context, err error) {
	if errors.Is(err, likes.ErrNotReady) {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "post like state is not ready"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func setPostLikedStateWithRedis(userID uint, postID uint, liked bool) (postLikeMutationResult, error) {
	state, err := likes.NewStore(global.RedisDB).Mutate(context.Background(), userID, postID, liked)
	if err != nil {
		return postLikeMutationResult{}, err
	}
	if state.Changed {
		if err := invalidatePostLikeDetailCache(postID); err != nil {
			log.Printf("[Like] invalidate post %d cache: %v", postID, err)
		}
	}
	return postLikeMutationResult{
		Likes: state.Count, Liked: state.Liked,
		ChangedToLiked: liked && state.Changed, ChangedToUnliked: !liked && state.Changed,
	}, nil
}

func loadPostLikeStateFromRedis(userID uint, postID uint) (postLikeStateResult, error) {
	state, err := likes.NewStore(global.RedisDB).Get(context.Background(), userID, postID)
	return postLikeStateResult{Likes: state.Count, Liked: state.Liked}, err
}

func loadPostLikeStatesFromRedis(userID uint, postIDs []uint) (postLikeStatesLoadResult, error) {
	states, unavailable, err := likes.NewStore(global.RedisDB).GetMany(context.Background(), userID, postIDs)
	if err != nil {
		return postLikeStatesLoadResult{}, err
	}
	result := postLikeStatesLoadResult{
		States:      make(map[uint]postLikeStateResult, len(states)),
		Unavailable: unavailable,
	}
	for postID, state := range states {
		result.States[postID] = postLikeStateResult{Likes: state.Count, Liked: state.Liked}
	}
	return result, nil
}
func getPostLikeCount(postID uint) (int64, error) {
	result, err := loadPostLikeStateFromRedis(0, postID)
	return result.Likes, err
}

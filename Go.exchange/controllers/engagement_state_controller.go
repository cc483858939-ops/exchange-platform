package controllers

import (
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

type engagementLikeState struct {
	Status string `json:"status"`
	Likes  *int64 `json:"likes,omitempty"`
	Liked  *bool  `json:"liked,omitempty"`
}

type engagementRepostState struct {
	Status   string `json:"status"`
	Reposts  *int64 `json:"reposts,omitempty"`
	Reposted *bool  `json:"reposted,omitempty"`
}

type engagementBookmarkState struct {
	Status     string `json:"status"`
	Bookmarked *bool  `json:"bookmarked,omitempty"`
}

type engagementStateItem struct {
	PostID   uint                    `json:"post_id"`
	Like     engagementLikeState     `json:"like"`
	Repost   engagementRepostState   `json:"repost"`
	Bookmark engagementBookmarkState `json:"bookmark"`
}

type engagementStatesResponse struct {
	Items []engagementStateItem `json:"items"`
}

// GetPostEngagementStates aggregates the three independent state loaders into
// one authenticated transport response without coupling their failure domains.
func GetPostEngagementStates(ctx *gin.Context) {
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
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	var likeResult postLikeStatesLoadResult
	var likeErr error
	var repostResult postRepostStatesLoadResult
	var repostErr error
	var bookmarkResult postBookmarkStatesLoadResult
	var bookmarkErr error
	var loaders sync.WaitGroup
	loaders.Add(3)
	requestContext := ctx.Request.Context()
	go func() {
		defer loaders.Done()
		likeResult, likeErr = loadPostLikeStates(requestContext, userID, uniqueIDs)
	}()
	go func() {
		defer loaders.Done()
		repostResult, repostErr = loadPostRepostStates(requestContext, userID, uniqueIDs)
	}()
	go func() {
		defer loaders.Done()
		bookmarkResult, bookmarkErr = loadPostBookmarkStates(requestContext, userID, uniqueIDs)
	}()
	loaders.Wait()

	if likeErr != nil {
		log.Printf("post engagement like state loader failed: %v", likeErr)
	}
	if repostErr != nil {
		log.Printf("post engagement repost state loader failed: %v", repostErr)
	}
	if bookmarkErr != nil {
		log.Printf("post engagement bookmark state loader failed: %v", bookmarkErr)
	}
	if likeErr != nil && repostErr != nil && bookmarkErr != nil {
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "Engagement states are temporarily unavailable"})
		return
	}

	response := engagementStatesResponse{Items: make([]engagementStateItem, 0, len(uniqueIDs))}
	for _, postID := range uniqueIDs {
		item := engagementStateItem{PostID: postID}
		if likeErr == nil {
			if state, ready := likeResult.States[postID]; ready {
				likes, liked := state.Likes, state.Liked
				item.Like = engagementLikeState{Status: "ready", Likes: &likes, Liked: &liked}
			} else {
				item.Like.Status = "unavailable"
			}
		} else {
			item.Like.Status = "unavailable"
		}
		if repostErr == nil {
			if state, ready := repostResult.States[postID]; ready {
				reposts, reposted := normalizePostRepostCount(state.Reposts), state.Reposted
				item.Repost = engagementRepostState{Status: "ready", Reposts: &reposts, Reposted: &reposted}
			} else {
				item.Repost.Status = "unavailable"
			}
		} else {
			item.Repost.Status = "unavailable"
		}
		if bookmarkErr == nil {
			if state, ready := bookmarkResult.States[postID]; ready {
				bookmarked := state.Bookmarked
				item.Bookmark = engagementBookmarkState{Status: "ready", Bookmarked: &bookmarked}
			} else {
				item.Bookmark.Status = "unavailable"
			}
		} else {
			item.Bookmark.Status = "unavailable"
		}
		response.Items = append(response.Items, item)
	}
	ctx.JSON(http.StatusOK, response)
}

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errPostBookmarkUnavailable = errors.New("post bookmark post unavailable")

type postBookmarkStateResult struct {
	PostID     uint
	Bookmarked bool
}

type postBookmarkStatesRequest struct {
	PostIDs []uint `json:"post_ids"`
}

type postBookmarkStateItem struct {
	PostID     uint `json:"post_id"`
	Bookmarked bool `json:"bookmarked"`
}

type postBookmarkStatesResponse struct {
	Items              []postBookmarkStateItem `json:"items"`
	UnavailablePostIDs []uint                  `json:"unavailable_post_ids"`
}

type postBookmarkStatesLoadResult struct {
	States      map[uint]postBookmarkStateResult
	Unavailable []uint
}

type postBookmarkMutationResult struct {
	PostID     uint `json:"post_id"`
	Bookmarked bool `json:"bookmarked"`
}

var loadPostBookmarkStates = loadPostBookmarkStatesFromDB
var mutatePostBookmark = mutatePostBookmarkFromDB
var loadPostBookmarkHistoryPage = loadPostBookmarkHistoryPageFromDB

func BookmarkPost(ctx *gin.Context) {
	mutatePostBookmarkRequest(ctx, true)
}

func UnbookmarkPost(ctx *gin.Context) {
	mutatePostBookmarkRequest(ctx, false)
}

func mutatePostBookmarkRequest(ctx *gin.Context, bookmarked bool) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	result, err := mutatePostBookmark(ctx.Request.Context(), userID, postID, bookmarked)
	if err != nil {
		writePostBookmarkError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func GetPostBookmarkStates(ctx *gin.Context) {
	var request postBookmarkStatesRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post_ids"})
		return
	}
	if len(request.PostIDs) == 0 || len(request.PostIDs) > maxPostBookmarkStateIDs {
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
	result, err := loadPostBookmarkStates(ctx.Request.Context(), userID, uniqueIDs)
	if err != nil {
		writePostBookmarkError(ctx, err)
		return
	}

	response := postBookmarkStatesResponse{
		Items:              make([]postBookmarkStateItem, 0, len(result.States)),
		UnavailablePostIDs: make([]uint, 0, len(result.Unavailable)),
	}
	unavailable := make(map[uint]struct{}, len(result.Unavailable))
	for _, postID := range result.Unavailable {
		unavailable[postID] = struct{}{}
	}
	for _, postID := range uniqueIDs {
		if state, available := result.States[postID]; available {
			response.Items = append(response.Items, postBookmarkStateItem{
				PostID:     postID,
				Bookmarked: state.Bookmarked,
			})
			continue
		}
		if _, markedUnavailable := unavailable[postID]; markedUnavailable {
			response.UnavailablePostIDs = append(response.UnavailablePostIDs, postID)
			continue
		}
		response.UnavailablePostIDs = append(response.UnavailablePostIDs, postID)
	}
	ctx.JSON(http.StatusOK, response)
}

func writePostBookmarkError(ctx *gin.Context, err error) {
	if handleRequestDBError(ctx, err) {
		return
	}
	if errors.Is(err, errPostBookmarkUnavailable) ||
		errors.Is(err, errPostRepostNotFound) ||
		errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func mutatePostBookmarkFromDB(ctx context.Context, userID, postID uint, bookmarked bool) (postBookmarkMutationResult, error) {
	if global.Db == nil {
		return postBookmarkMutationResult{}, errors.New("database is not initialized")
	}
	db := global.Db.WithContext(ctx)
	if userID == 0 || postID == 0 {
		return postBookmarkMutationResult{}, errPostBookmarkUnavailable
	}

	if bookmarked {
		if err := requirePublicPost(db, postID, time.Now().UTC()); err != nil {
			return postBookmarkMutationResult{}, err
		}
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "post_id"}},
			DoNothing: true,
		}).Create(&models.PostBookmark{UserID: userID, PostID: postID}).Error; err != nil {
			return postBookmarkMutationResult{}, err
		}
		return postBookmarkMutationResult{PostID: postID, Bookmarked: true}, nil
	}

	if err := db.Where("user_id = ? AND post_id = ?", userID, postID).
		Delete(&models.PostBookmark{}).Error; err != nil {
		return postBookmarkMutationResult{}, err
	}
	return postBookmarkMutationResult{PostID: postID, Bookmarked: false}, nil
}

func loadPostBookmarkStatesFromDB(ctx context.Context, userID uint, postIDs []uint) (postBookmarkStatesLoadResult, error) {
	result := postBookmarkStatesLoadResult{
		States:      make(map[uint]postBookmarkStateResult, len(postIDs)),
		Unavailable: make([]uint, 0),
	}
	if global.Db == nil {
		return result, errors.New("database is not initialized")
	}
	db := global.Db.WithContext(ctx)
	if userID == 0 {
		return result, errors.New("invalid bookmark viewer")
	}
	if len(postIDs) == 0 {
		return result, nil
	}

	now := time.Now().UTC()
	var availableIDs []uint
	if err := publicPostScope(db.Model(&models.Post{}), now).
		Where("posts.id IN ?", postIDs).
		Pluck("posts.id", &availableIDs).Error; err != nil {
		return postBookmarkStatesLoadResult{}, err
	}
	available := make(map[uint]struct{}, len(availableIDs))
	for _, postID := range availableIDs {
		available[postID] = struct{}{}
		result.States[postID] = postBookmarkStateResult{PostID: postID}
	}

	if len(availableIDs) > 0 {
		var bookmarkedIDs []uint
		if err := db.Model(&models.PostBookmark{}).
			Where("user_id = ? AND post_id IN ?", userID, availableIDs).
			Pluck("post_id", &bookmarkedIDs).Error; err != nil {
			return postBookmarkStatesLoadResult{}, err
		}
		for _, postID := range bookmarkedIDs {
			state := result.States[postID]
			state.Bookmarked = true
			result.States[postID] = state
		}
	}

	for _, postID := range postIDs {
		if _, ok := available[postID]; !ok {
			result.Unavailable = append(result.Unavailable, postID)
		}
	}
	return result, nil
}

func loadPostBookmarkHistoryPageFromDB(ctx context.Context, viewerID uint, limit int, cursor *bookmarkHistoryCursor) (postPageResponse, error) {
	if global.Db == nil {
		return postPageResponse{}, errors.New("database is not initialized")
	}
	if viewerID == 0 || limit <= 0 {
		return postPageResponse{}, errors.New("invalid bookmark history query")
	}

	var response postPageResponse
	err := global.Db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY").Error; err != nil {
			return err
		}

		now := time.Now().UTC()
		membershipQuery := tx.Table("post_bookmarks AS bookmark").
			Select("bookmark.post_id, bookmark.created_at").
			Joins("JOIN posts ON posts.id = bookmark.post_id").
			Where("bookmark.user_id = ?", viewerID).
			Scopes(func(query *gorm.DB) *gorm.DB { return publicPostScope(query, now) })
		if cursor != nil {
			membershipQuery = membershipQuery.Where(
				"(bookmark.created_at < ?) OR (bookmark.created_at = ? AND bookmark.post_id > ?)",
				cursor.BookmarkedAt,
				cursor.BookmarkedAt,
				cursor.PostID,
			)
		}

		var membershipRows []bookmarkHistoryMembershipRow
		if err := membershipQuery.
			Order("bookmark.created_at DESC, bookmark.post_id ASC").
			Limit(limit + 1).
			Scan(&membershipRows).Error; err != nil {
			return err
		}

		hasMore := len(membershipRows) > limit
		if hasMore {
			membershipRows = membershipRows[:limit]
		}
		if len(membershipRows) == 0 {
			response = postPageResponse{Items: make([]postResponse, 0)}
			return nil
		}

		postIDs := make([]uint, 0, len(membershipRows))
		for _, row := range membershipRows {
			postIDs = append(postIDs, row.PostID)
		}
		posts, err := loadPostResponses(
			tx.Model(&models.Post{}).
				Select(publicPostSelectColumns).
				Where("posts.id IN ?", postIDs).
				Scopes(func(query *gorm.DB) *gorm.DB { return publicPostScope(query, now) }),
		)
		if err != nil {
			return err
		}

		postsByID := make(map[uint]postResponse, len(posts))
		for _, post := range posts {
			postsByID[post.ID] = post
		}
		items := make([]postResponse, 0, len(membershipRows))
		for _, row := range membershipRows {
			post, ok := postsByID[row.PostID]
			if !ok {
				return fmt.Errorf("bookmark history post %d could not be loaded", row.PostID)
			}
			items = append(items, post)
		}

		response = postPageResponse{Items: items}
		if !hasMore {
			return nil
		}
		nextCursor, err := encodeBookmarkHistoryCursor(bookmarkHistoryCursor{
			Version:      bookmarkHistoryCursorVersion,
			BookmarkedAt: membershipRows[len(membershipRows)-1].CreatedAt,
			PostID:       membershipRows[len(membershipRows)-1].PostID,
		})
		if err != nil {
			return err
		}
		response.NextCursor = &nextCursor
		return nil
	})
	if err != nil {
		return postPageResponse{}, err
	}
	return response, nil
}

func GetMyBookmarks(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	limit, cursor, err := parseBookmarkHistoryPageQuery(ctx)
	if err != nil {
		writeBookmarkHistoryQueryError(ctx, err)
		return
	}
	response, err := loadPostBookmarkHistoryPage(ctx.Request.Context(), viewerID, limit, cursor)
	if err != nil {
		writePostTimelineStoreError(ctx, err)
		return
	}
	if response.Items == nil {
		response.Items = make([]postResponse, 0)
	}
	ctx.JSON(http.StatusOK, response)
}

type bookmarkHistoryMembershipRow struct {
	PostID    uint      `gorm:"column:post_id"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

const maxPostBookmarkStateIDs = 100

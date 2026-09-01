package controllers

import (
	"errors"
	"net/http"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errPostRepostNotFound = errors.New("post not found")

type postRepostStateResult struct {
	Reposts  int64
	Reposted bool
}

type postRepostMutationResult = postRepostStateResult

type postRepostStatesRequest struct {
	PostIDs []uint `json:"post_ids"`
}

type postRepostStateItem struct {
	PostID   uint  `json:"post_id"`
	Reposts  int64 `json:"reposts"`
	Reposted bool  `json:"reposted"`
}

type postRepostStatesResponse struct {
	Items              []postRepostStateItem `json:"items"`
	UnavailablePostIDs []uint                `json:"unavailable_post_ids"`
}

type postRepostStatesLoadResult struct {
	States      map[uint]postRepostStateResult
	Unavailable []uint
}

type postRepostCountRow struct {
	PostID  uint  `gorm:"column:post_id"`
	Reposts int64 `gorm:"column:reposts"`
}

const maxPostRepostStateIDs = 100

var loadPostRepostState = loadPostRepostStateFromDB
var mutatePostRepost = mutatePostRepostFromDB
var loadPostRepostStates = loadPostRepostStatesFromDB

func GetPostRepostState(ctx *gin.Context) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	result, err := loadPostRepostState(userID, postID)
	if err != nil {
		writePostRepostError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, postRepostStatePayload(result))
}

// GetPostRepost is kept as a descriptive handler alias for route wiring and tests.
func GetPostRepost(ctx *gin.Context) {
	GetPostRepostState(ctx)
}

func RepostPost(ctx *gin.Context) {
	mutatePostRepostRequest(ctx, true)
}

func UndoRepostPost(ctx *gin.Context) {
	mutatePostRepostRequest(ctx, false)
}

func mutatePostRepostRequest(ctx *gin.Context, reposted bool) {
	postID, ok := postIDFromContext(ctx)
	if !ok {
		return
	}
	userID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return
	}

	result, err := mutatePostRepost(userID, postID, reposted)
	if err != nil {
		writePostRepostError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, postRepostStatePayload(result))
}

func GetPostRepostStates(ctx *gin.Context) {
	var request postRepostStatesRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid post_ids"})
		return
	}
	if len(request.PostIDs) == 0 || len(request.PostIDs) > maxPostRepostStateIDs {
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
	result, err := loadPostRepostStates(userID, uniqueIDs)
	if err != nil {
		writePostRepostError(ctx, err)
		return
	}

	response := postRepostStatesResponse{
		Items:              make([]postRepostStateItem, 0, len(result.States)),
		UnavailablePostIDs: make([]uint, 0, len(result.Unavailable)),
	}
	unavailable := make(map[uint]struct{}, len(result.Unavailable))
	for _, postID := range result.Unavailable {
		unavailable[postID] = struct{}{}
	}
	for _, postID := range uniqueIDs {
		if state, available := result.States[postID]; available {
			response.Items = append(response.Items, postRepostStateItem{
				PostID:   postID,
				Reposts:  normalizePostRepostCount(state.Reposts),
				Reposted: state.Reposted,
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

func postRepostStatePayload(result postRepostStateResult) gin.H {
	return gin.H{
		"reposts":  normalizePostRepostCount(result.Reposts),
		"reposted": result.Reposted,
	}
}

func normalizePostRepostCount(count int64) int64 {
	if count < 0 {
		return 0
	}
	return count
}

func activePostRepostScope(db *gorm.DB) *gorm.DB {
	return db.
		Table("post_reposts AS ar").
		Joins(`
			JOIN users AS repost_users
			  ON repost_users.id = ar.user_id
			 AND repost_users.deleted_at IS NULL
		`)
}

func writePostRepostError(ctx *gin.Context, err error) {
	if errors.Is(err, errPostRepostNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "post not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func loadPostRepostStateFromDB(userID, postID uint) (postRepostStateResult, error) {
	if global.Db == nil {
		return postRepostStateResult{}, errors.New("database is not initialized")
	}
	return loadPostRepostStateWithDB(global.Db, userID, postID, time.Now().UTC())
}

func loadPostRepostStateWithDB(db *gorm.DB, userID, postID uint, now time.Time) (postRepostStateResult, error) {
	if err := requirePublicPost(db, postID, now); err != nil {
		return postRepostStateResult{}, err
	}

	var reposts int64
	if err := activePostRepostScope(db).
		Where("ar.post_id = ?", postID).
		Count(&reposts).Error; err != nil {
		return postRepostStateResult{}, err
	}

	var relation models.PostRepost
	err := db.Where("user_id = ? AND post_id = ?", userID, postID).
		Select("id").First(&relation).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return postRepostStateResult{}, err
	}
	return postRepostStateResult{
		Reposts:  normalizePostRepostCount(reposts),
		Reposted: err == nil,
	}, nil
}

func mutatePostRepostFromDB(userID, postID uint, reposted bool) (postRepostMutationResult, error) {
	if global.Db == nil {
		return postRepostMutationResult{}, errors.New("database is not initialized")
	}

	var result postRepostMutationResult
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := requirePublicPost(tx, postID, time.Now().UTC()); err != nil {
			return err
		}

		if reposted {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "user_id"}, {Name: "post_id"}},
				DoNothing: true,
			}).Create(&models.PostRepost{UserID: userID, PostID: postID}).Error; err != nil {
				return err
			}
		} else if err := tx.Where("user_id = ? AND post_id = ?", userID, postID).
			Delete(&models.PostRepost{}).Error; err != nil {
			return err
		}

		var err error
		result, err = loadPostRepostStateWithDB(tx, userID, postID, time.Now().UTC())
		return err
	})
	return result, err
}

func loadPostRepostStatesFromDB(userID uint, postIDs []uint) (postRepostStatesLoadResult, error) {
	result := postRepostStatesLoadResult{
		States:      make(map[uint]postRepostStateResult, len(postIDs)),
		Unavailable: make([]uint, 0),
	}
	if global.Db == nil {
		return result, errors.New("database is not initialized")
	}
	if len(postIDs) == 0 {
		return result, nil
	}

	now := time.Now().UTC()
	var availableIDs []uint
	if err := publicPostScope(global.Db.Model(&models.Post{}), now).
		Where("posts.id IN ?", postIDs).
		Pluck("posts.id", &availableIDs).Error; err != nil {
		return postRepostStatesLoadResult{}, err
	}
	available := make(map[uint]struct{}, len(availableIDs))
	for _, postID := range availableIDs {
		available[postID] = struct{}{}
		result.States[postID] = postRepostStateResult{}
	}

	if len(availableIDs) > 0 {
		var counts []postRepostCountRow
		if err := activePostRepostScope(global.Db).
			Select("ar.post_id, COUNT(*) AS reposts").
			Where("ar.post_id IN ?", availableIDs).
			Group("ar.post_id").
			Scan(&counts).Error; err != nil {
			return postRepostStatesLoadResult{}, err
		}
		for _, count := range counts {
			state := result.States[count.PostID]
			state.Reposts = normalizePostRepostCount(count.Reposts)
			result.States[count.PostID] = state
		}

		var repostedIDs []uint
		if err := global.Db.Model(&models.PostRepost{}).
			Where("user_id = ? AND post_id IN ?", userID, availableIDs).
			Pluck("post_id", &repostedIDs).Error; err != nil {
			return postRepostStatesLoadResult{}, err
		}
		for _, postID := range repostedIDs {
			state := result.States[postID]
			state.Reposted = true
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

func requirePublicPost(db *gorm.DB, postID uint, now time.Time) error {
	if db == nil {
		return errors.New("database is not initialized")
	}
	if postID == 0 {
		return errPostRepostNotFound
	}

	var id uint
	err := publicPostScope(db.Model(&models.Post{}), now).
		Where("posts.id = ?", postID).
		Select("posts.id").
		Take(&id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errPostRepostNotFound
	}
	return err
}

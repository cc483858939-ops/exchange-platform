package controllers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type userFollowStateResponse struct {
	UserID         uint  `json:"user_id"`
	Following      bool  `json:"following"`
	FollowerCount  int64 `json:"follower_count"`
	FollowingCount int64 `json:"following_count"`
}

type userFollowState struct {
	Following      bool
	FollowerCount  int64
	FollowingCount int64
}

const (
	defaultFollowListLimit = 20
	maxFollowListLimit     = 50
)

type followConnectionKind uint8

const (
	followConnectionFollowers followConnectionKind = iota
	followConnectionFollowing
)

type userConnectionResponse struct {
	User      publicUserResponse `json:"user"`
	Following bool               `json:"following"`
}

type userConnectionPageResponse struct {
	Items   []userConnectionResponse `json:"items"`
	HasMore bool                     `json:"has_more"`
}

type userConnectionQueryRow struct {
	UserID         uint      `gorm:"column:user_id"`
	Username       string    `gorm:"column:username"`
	DisplayName    string    `gorm:"column:display_name"`
	Bio            string    `gorm:"column:bio"`
	AvatarURL      string    `gorm:"column:avatar_url"`
	UserCreatedAt  time.Time `gorm:"column:user_created_at"`
	ViewerFollowID *uint     `gorm:"column:viewer_follow_id"`
}

var loadActiveFollowUser = loadActiveFollowUserFromDB
var loadFollowState = loadFollowStateFromDB
var followAndLoadState = followAndLoadStateFromDB
var unfollowAndLoadState = unfollowAndLoadStateFromDB

var loadUserConnections = loadUserConnectionsFromDB

func loadActiveFollowUserFromDB(id uint) error {
	if global.Db == nil {
		return errors.New("database is not initialized")
	}
	var user models.User
	return global.Db.Select("id").First(&user, id).Error
}

func readFollowState(db *gorm.DB, viewerID, targetID uint) (userFollowState, error) {
	if db == nil {
		return userFollowState{}, errors.New("database is not initialized")
	}

	state := userFollowState{}
	if viewerID != targetID {
		var relationCount int64
		if err := db.Model(&models.UserFollow{}).
			Where("follower_id = ? AND following_id = ?", viewerID, targetID).
			Count(&relationCount).Error; err != nil {
			return userFollowState{}, err
		}
		state.Following = relationCount > 0
	}

	if err := db.Table("user_follows AS uf").
		Joins("JOIN users AS follower ON follower.id = uf.follower_id AND follower.deleted_at IS NULL").
		Where("uf.following_id = ?", targetID).
		Count(&state.FollowerCount).Error; err != nil {
		return userFollowState{}, err
	}
	if err := db.Table("user_follows AS uf").
		Joins("JOIN users AS followed ON followed.id = uf.following_id AND followed.deleted_at IS NULL").
		Where("uf.follower_id = ?", targetID).
		Count(&state.FollowingCount).Error; err != nil {
		return userFollowState{}, err
	}
	return state, nil
}

func loadFollowStateFromDB(viewerID, targetID uint) (userFollowState, error) {
	return readFollowState(global.Db, viewerID, targetID)
}

func loadUserConnectionsFromDB(viewerID, targetID uint, kind followConnectionKind, limit, offset int) (userConnectionPageResponse, error) {
	if global.Db == nil {
		return userConnectionPageResponse{}, errors.New("database is not initialized")
	}
	listedUserJoin := "JOIN users AS listed_user ON listed_user.id = target_follow.follower_id AND listed_user.deleted_at IS NULL"
	switch kind {
	case followConnectionFollowers:
	case followConnectionFollowing:
		listedUserJoin = "JOIN users AS listed_user ON listed_user.id = target_follow.following_id AND listed_user.deleted_at IS NULL"
	default:
		return userConnectionPageResponse{}, errors.New("invalid follow connection kind")
	}
	query := global.Db.Table("user_follows AS target_follow").
		Select(`listed_user.id AS user_id, listed_user.username AS username, listed_user.display_name AS display_name, listed_user.bio AS bio, listed_user.avatar_url AS avatar_url, listed_user.created_at AS user_created_at, viewer_follow.id AS viewer_follow_id`).
		Joins(listedUserJoin).
		Joins("LEFT JOIN user_follows AS viewer_follow ON viewer_follow.follower_id = ? AND viewer_follow.following_id = listed_user.id", viewerID).
		Order("target_follow.created_at DESC").Order("target_follow.id DESC").Limit(limit + 1).Offset(offset)
	if kind == followConnectionFollowers {
		query = query.Where("target_follow.following_id = ?", targetID)
	} else {
		query = query.Where("target_follow.follower_id = ?", targetID)
	}
	var rows []userConnectionQueryRow
	if err := query.Scan(&rows).Error; err != nil {
		return userConnectionPageResponse{}, err
	}
	page := userConnectionPageResponse{Items: make([]userConnectionResponse, 0, len(rows))}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, userConnectionResponse{
			User:      publicUserResponse{ID: row.UserID, Username: row.Username, DisplayName: row.DisplayName, Bio: row.Bio, AvatarURL: row.AvatarURL, CreatedAt: row.UserCreatedAt},
			Following: row.UserID != viewerID && row.ViewerFollowID != nil,
		})
	}
	return page, nil
}

func followAndLoadStateFromDB(viewerID, targetID uint) (userFollowState, error) {
	if global.Db == nil {
		return userFollowState{}, errors.New("database is not initialized")
	}

	var state userFollowState
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "follower_id"}, {Name: "following_id"}},
			DoNothing: true,
		}).Create(&models.UserFollow{
			FollowerID:  viewerID,
			FollowingID: targetID,
		}).Error; err != nil {
			return err
		}
		nextState, err := readFollowState(tx, viewerID, targetID)
		if err != nil {
			return err
		}
		state = nextState
		return nil
	})
	return state, err
}

func unfollowAndLoadStateFromDB(viewerID, targetID uint) (userFollowState, error) {
	if global.Db == nil {
		return userFollowState{}, errors.New("database is not initialized")
	}

	var state userFollowState
	err := global.Db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where(
			"follower_id = ? AND following_id = ?",
			viewerID,
			targetID,
		).Delete(&models.UserFollow{}).Error; err != nil {
			return err
		}
		nextState, err := readFollowState(tx, viewerID, targetID)
		if err != nil {
			return err
		}
		state = nextState
		return nil
	})
	return state, err
}

func validateFollowParticipants(ctx *gin.Context) (uint, uint, bool) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return 0, 0, false
	}

	targetID, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return 0, 0, false
	}

	if err := loadActiveFollowUser(viewerID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		} else {
			writeFollowStoreError(ctx)
		}
		return 0, 0, false
	}
	if err := loadActiveFollowUser(targetID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		} else {
			writeFollowStoreError(ctx)
		}
		return 0, 0, false
	}
	return viewerID, targetID, true
}

func parseFollowListPagination(ctx *gin.Context) (int, int, error) {
	limit := defaultFollowListLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxFollowListLimit {
		limit = maxFollowListLimit
	}
	offset := 0
	if raw, exists := ctx.GetQuery("offset"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("invalid offset")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func writeFollowStoreError(ctx *gin.Context) {
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func writeFollowState(ctx *gin.Context, targetID uint, state userFollowState) {
	ctx.JSON(http.StatusOK, userFollowStateResponse{
		UserID:         targetID,
		Following:      state.Following,
		FollowerCount:  state.FollowerCount,
		FollowingCount: state.FollowingCount,
	})
}

func GetUserFollowState(ctx *gin.Context) {
	viewerID, targetID, ok := validateFollowParticipants(ctx)
	if !ok {
		return
	}
	state, err := loadFollowState(viewerID, targetID)
	if err != nil {
		writeFollowStoreError(ctx)
		return
	}
	if viewerID == targetID {
		state.Following = false
	}
	writeFollowState(ctx, targetID, state)
}

func FollowUser(ctx *gin.Context) {
	viewerID, targetID, ok := validateFollowParticipants(ctx)
	if !ok {
		return
	}
	if viewerID == targetID {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "cannot follow yourself"})
		return
	}
	state, err := followAndLoadState(viewerID, targetID)
	if err != nil {
		writeFollowStoreError(ctx)
		return
	}
	writeFollowState(ctx, targetID, state)
}

func UnfollowUser(ctx *gin.Context) {
	viewerID, targetID, ok := validateFollowParticipants(ctx)
	if !ok {
		return
	}
	if viewerID == targetID {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "cannot unfollow yourself"})
		return
	}
	state, err := unfollowAndLoadState(viewerID, targetID)
	if err != nil {
		writeFollowStoreError(ctx)
		return
	}
	writeFollowState(ctx, targetID, state)
}
func GetUserFollowers(ctx *gin.Context) {
	getUserConnections(ctx, followConnectionFollowers)
}

func GetUserFollowing(ctx *gin.Context) {
	getUserConnections(ctx, followConnectionFollowing)
}

func getUserConnections(ctx *gin.Context, kind followConnectionKind) {
	viewerID, targetID, ok := validateFollowParticipants(ctx)
	if !ok {
		return
	}
	limit, offset, err := parseFollowListPagination(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, err := loadUserConnections(viewerID, targetID, kind, limit, offset)
	if err != nil {
		writeFollowStoreError(ctx)
		return
	}
	if page.Items == nil {
		page.Items = make([]userConnectionResponse, 0)
	}
	ctx.JSON(http.StatusOK, page)
}

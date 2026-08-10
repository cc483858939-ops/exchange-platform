package controllers

import (
	"errors"
	"net/http"

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

var loadActiveFollowUser = loadActiveFollowUserFromDB
var loadFollowState = loadFollowStateFromDB
var followAndLoadState = followAndLoadStateFromDB
var unfollowAndLoadState = unfollowAndLoadStateFromDB

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

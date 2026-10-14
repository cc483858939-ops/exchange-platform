package controllers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultUserMuteLimit  = 20
	maxUserMuteLimit      = 50
	userMuteCursorVersion = 1
)

type userMuteCursor struct {
	Version   int       `json:"v"`
	CreatedAt time.Time `json:"created_at"`
	ID        uint      `json:"id"`
}

type mutedUserSummary struct {
	ID          uint   `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

type userMuteItemResponse struct {
	User      mutedUserSummary `json:"user"`
	CreatedAt time.Time        `json:"created_at"`
}

type userMutePageResponse struct {
	Items      []userMuteItemResponse `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

type userMuteQueryRow struct {
	ID          uint      `gorm:"column:id"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UserID      uint      `gorm:"column:user_id"`
	Username    string    `gorm:"column:username"`
	DisplayName string    `gorm:"column:display_name"`
	AvatarURL   string    `gorm:"column:avatar_url"`
}

var loadActiveMuteUser = loadActiveMuteUserFromDB
var createUserMute = createUserMuteInDB
var deleteUserMute = deleteUserMuteFromDB
var loadUserMutes = loadUserMutesFromDB

func loadActiveMuteUserFromDB(ctx context.Context, userID uint) error {
	if global.APIDb == nil {
		return errors.New("database is not initialized")
	}
	var user models.User
	return global.APIDb.WithContext(ctx).Select("id").First(&user, userID).Error
}

func createUserMuteInDB(ctx context.Context, muterID, mutedID uint) error {
	if global.APIDb == nil {
		return errors.New("database is not initialized")
	}
	relation := models.UserMute{MuterID: muterID, MutedID: mutedID}
	return global.APIDb.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "muter_id"}, {Name: "muted_id"}},
		DoNothing: true,
	}).Create(&relation).Error
}

func deleteUserMuteFromDB(ctx context.Context, muterID, mutedID uint) error {
	if global.APIDb == nil {
		return errors.New("database is not initialized")
	}
	return global.APIDb.WithContext(ctx).Where("muter_id = ? AND muted_id = ?", muterID, mutedID).
		Delete(&models.UserMute{}).Error
}

func parseUserMuteCursor(raw string) (*userMuteCursor, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("invalid cursor")
	}
	var cursor userMuteCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Version != userMuteCursorVersion || cursor.ID == 0 || cursor.CreatedAt.IsZero() {
		return nil, errors.New("invalid cursor")
	}
	return &cursor, nil
}

func encodeUserMuteCursor(cursor userMuteCursor) (string, error) {
	cursor.Version = userMuteCursorVersion
	if cursor.ID == 0 || cursor.CreatedAt.IsZero() {
		return "", errors.New("invalid user mute cursor")
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func loadUserMutesFromDB(ctx context.Context, viewerID uint, limit int, cursor *userMuteCursor) (userMutePageResponse, error) {
	if global.APIDb == nil {
		return userMutePageResponse{}, errors.New("database is not initialized")
	}
	query := global.APIDb.WithContext(ctx).Table("user_mutes AS um").
		Select("um.id, um.created_at, muted.id AS user_id, muted.username, muted.display_name, muted.avatar_url").
		Joins("JOIN users AS muted ON muted.id = um.muted_id AND muted.deleted_at IS NULL").
		Where("um.muter_id = ?", viewerID)
	if cursor != nil {
		query = query.Where("(um.created_at < ?) OR (um.created_at = ? AND um.id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var rows []userMuteQueryRow
	if err := query.Order("um.created_at DESC, um.id DESC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		return userMutePageResponse{}, err
	}
	response := userMutePageResponse{Items: make([]userMuteItemResponse, 0, minInt(len(rows), limit))}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		response.Items = append(response.Items, userMuteItemResponse{
			User: mutedUserSummary{
				ID: row.UserID, Username: row.Username, DisplayName: row.DisplayName, AvatarURL: row.AvatarURL,
			},
			CreatedAt: row.CreatedAt,
		})
	}
	if hasMore && len(rows) > 0 {
		next, err := encodeUserMuteCursor(userMuteCursor{CreatedAt: rows[len(rows)-1].CreatedAt, ID: rows[len(rows)-1].ID})
		if err != nil {
			return userMutePageResponse{}, err
		}
		response.NextCursor = &next
	}
	return response, nil
}

func muteViewerID(ctx *gin.Context) (uint, bool) {
	viewerID, ok := userIDFromContext(ctx)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing user"})
		return 0, false
	}
	if _, err := loadActiveProfileViewer(ctx.Request.Context(), viewerID); err != nil {
		if handleRequestDBError(ctx, err) {
			return 0, false
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "viewer is no longer active"})
		} else {
			writeUserMuteStoreError(ctx, err)
		}
		return 0, false
	}
	return viewerID, true
}

func parseMuteTarget(ctx *gin.Context) (uint, bool) {
	targetID, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return 0, false
	}
	return targetID, true
}

func writeUserMuteStoreError(ctx *gin.Context, err error) {
	if handleRequestDBError(ctx, err) {
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
}

func MuteUser(ctx *gin.Context) {
	viewerID, ok := muteViewerID(ctx)
	if !ok {
		return
	}
	targetID, ok := parseMuteTarget(ctx)
	if !ok {
		return
	}
	if viewerID == targetID {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "cannot mute yourself"})
		return
	}
	if err := loadActiveMuteUser(ctx.Request.Context(), targetID); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		} else {
			writeUserMuteStoreError(ctx, err)
		}
		return
	}
	if err := createUserMute(ctx.Request.Context(), viewerID, targetID); err != nil {
		writeUserMuteStoreError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func UnmuteUser(ctx *gin.Context) {
	viewerID, ok := muteViewerID(ctx)
	if !ok {
		return
	}
	targetID, ok := parseMuteTarget(ctx)
	if !ok {
		return
	}
	if err := deleteUserMute(ctx.Request.Context(), viewerID, targetID); err != nil {
		writeUserMuteStoreError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func GetMyMutedUsers(ctx *gin.Context) {
	viewerID, ok := muteViewerID(ctx)
	if !ok {
		return
	}
	limit, err := parsePageLimit(ctx, defaultUserMuteLimit, maxUserMuteLimit)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cursor, err := parseUserMuteCursor(ctx.Query("cursor"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, err := loadUserMutes(ctx.Request.Context(), viewerID, limit, cursor)
	if err != nil {
		writeUserMuteStoreError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

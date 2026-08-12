package controllers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	defaultUserArticleLimit = 20
	maxUserArticleLimit     = 50
	maxProfileDisplayRunes  = 50
	maxProfileBioRunes      = 160
)

var loadActiveProfileViewer = func(userID uint) (models.User, error) {
	if userID == 0 || global.Db == nil {
		return models.User{}, errors.New("database is not initialized")
	}

	var user models.User
	if err := global.Db.Select("id").First(&user, userID).Error; err != nil {
		return models.User{}, err
	}
	return user, nil
}

func requireActiveProfileViewerID(ctx *gin.Context) (uint, bool) {
	rawViewerID, exists := ctx.Get("user_id")
	if !exists {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing viewer identity"})
		return 0, false
	}
	viewerID, ok := jwtUserIDClaim(rawViewerID)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "missing viewer identity"})
		return 0, false
	}

	if _, err := loadActiveProfileViewer(viewerID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "viewer is no longer active"})
		} else {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return 0, false
	}
	return viewerID, true
}

func parsePublicUserID(raw string) (uint, error) {
	id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 0)
	if err != nil || id == 0 {
		return 0, errors.New("invalid user id")
	}
	return uint(id), nil
}

func parseUserArticlePagination(ctx *gin.Context) (int, int, error) {
	limit := defaultUserArticleLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxUserArticleLimit {
		limit = maxUserArticleLimit
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

func writeUserAPIError(ctx *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func decodeUserProfilePatch(reader io.Reader, viewerID uint) (map[string]any, error) {
	decoder := json.NewDecoder(reader)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, errors.New("malformed JSON body")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("malformed JSON body")
	}

	object := bytes.TrimSpace(raw)
	if len(object) < 2 || object[0] != '{' || object[len(object)-1] != '}' {
		return nil, errors.New("profile body must be a JSON object")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object, &fields); err != nil || fields == nil {
		return nil, errors.New("profile body must be a JSON object")
	}
	if len(fields) == 0 {
		return nil, errors.New("at least one profile field is required")
	}
	if _, exists := fields["username"]; exists {
		return nil, errors.New("username is not editable")
	}
	for field := range fields {
		if field != "display_name" && field != "bio" && field != "avatar_url" {
			return nil, fmt.Errorf("unknown profile field: %s", field)
		}
	}

	updates := make(map[string]any, len(fields))
	if rawValue, exists := fields["display_name"]; exists {
		value, err := decodeProfileText(rawValue, "display_name", maxProfileDisplayRunes)
		if err != nil {
			return nil, err
		}
		updates["display_name"] = value
	}
	if rawValue, exists := fields["bio"]; exists {
		value, err := decodeProfileText(rawValue, "bio", maxProfileBioRunes)
		if err != nil {
			return nil, err
		}
		updates["bio"] = value
	}
	if rawValue, exists := fields["avatar_url"]; exists {
		value, err := decodeProfileAvatarURL(rawValue, viewerID)
		if err != nil {
			return nil, err
		}
		updates["avatar_url"] = value
	}
	return updates, nil
}

func decodeProfileText(raw json.RawMessage, field string, maxRunes int) (string, error) {
	value := bytes.TrimSpace(raw)
	if bytes.Equal(value, []byte("null")) {
		return "", fmt.Errorf("%s cannot be null", field)
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", fmt.Errorf("%s must be a string", field)
	}
	decoded = strings.TrimSpace(decoded)
	if utf8.RuneCountInString(decoded) > maxRunes {
		return "", fmt.Errorf("%s is too long", field)
	}
	return decoded, nil
}

func decodeProfileAvatarURL(raw json.RawMessage, viewerID uint) (string, error) {
	value := bytes.TrimSpace(raw)
	if bytes.Equal(value, []byte("null")) {
		return "", errors.New("avatar_url cannot be null")
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", errors.New("avatar_url must be a string")
	}
	if decoded == "" {
		return "", nil
	}
	if err := validateProfileAvatarURL(decoded, viewerID); err != nil {
		return "", err
	}
	return decoded, nil
}

func validateProfileAvatarURL(value string, viewerID uint) error {
	if viewerID == 0 || strings.ContainsAny(value, "\r\n") || strings.Contains(value, "..") {
		return errors.New("invalid avatar_url")
	}
	prefix := fmt.Sprintf("/api/files/profile-avatars/%d/", viewerID)
	if !strings.HasPrefix(value, prefix) {
		return errors.New("avatar_url must belong to the current user")
	}
	filename := strings.TrimPrefix(value, prefix)
	if filename == "" || strings.ContainsAny(filename, "/\\") {
		return errors.New("invalid avatar_url")
	}

	var extension string
	for _, candidate := range []string{".jpg", ".png", ".webp"} {
		if strings.HasSuffix(filename, candidate) {
			extension = candidate
			break
		}
	}
	if extension == "" {
		return errors.New("invalid avatar_url extension")
	}

	uuidValue := strings.TrimSuffix(filename, extension)
	parsed, err := uuid.Parse(uuidValue)
	if err != nil || parsed.String() != uuidValue {
		return errors.New("invalid avatar_url filename")
	}
	return nil
}

func UpdateUserProfile(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	targetID, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if targetID != viewerID {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "you can only edit your own profile"})
		return
	}

	updates, err := decodeUserProfilePatch(ctx.Request.Body, viewerID)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if global.Db == nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "database is not initialized"})
		return
	}
	if result := global.Db.Model(&models.User{}).Where("id = ?", viewerID).Updates(updates); result.Error != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": result.Error.Error()})
		return
	}

	updated, err := loadPublicUserByID(viewerID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, updated)
}

func GetUserByID(ctx *gin.Context) {
	id, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, err := loadPublicUserByID(id)
	if err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, user)
}

func GetUserArticles(ctx *gin.Context) {
	id, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, offset, err := parseUserArticlePagination(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := loadPublicUserByID(id); err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	if global.Db == nil {
		writeUserAPIError(ctx, errors.New("database is not initialized"))
		return
	}
	query := global.Db.
		Select(articleListSelectColumns).
		Where("author_id = ?", id).
		Scopes(func(tx *gorm.DB) *gorm.DB { return visibleArticleScope(tx, time.Now()) }).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(offset)
	articles, err := loadArticleResponses(query)
	if err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, articles)
}

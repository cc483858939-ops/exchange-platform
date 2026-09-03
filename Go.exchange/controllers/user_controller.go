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
	"gorm.io/gorm/clause"
)

const (
	defaultUserPostLimit   = 20
	maxUserPostLimit       = 50
	maxProfileDisplayRunes = 50
	maxProfileBioRunes     = 160
)

const (
	defaultUserSearchLimit  = 20
	maxUserSearchLimit      = 50
	maxUserSearchQueryRunes = 50
)

type userSearchQueryRow struct {
	UserID         uint      `gorm:"column:user_id"`
	Username       string    `gorm:"column:username"`
	DisplayName    string    `gorm:"column:display_name"`
	Bio            string    `gorm:"column:bio"`
	AvatarURL      string    `gorm:"column:avatar_url"`
	UserCreatedAt  time.Time `gorm:"column:user_created_at"`
	ViewerFollowID *uint     `gorm:"column:viewer_follow_id"`
}

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

var searchUsers = searchUsersFromDB

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

func normalizeUserSearchQuery(raw string) (string, error) {
	query := strings.TrimSpace(raw)
	if strings.HasPrefix(query, "@") {
		query = strings.TrimSpace(strings.TrimPrefix(query, "@"))
	}
	if query == "" {
		return "", errors.New("search query is required")
	}
	if utf8.RuneCountInString(query) > maxUserSearchQueryRunes {
		return "", errors.New("search query is too long")
	}
	return query, nil
}

func parseUserSearchPagination(ctx *gin.Context) (int, int, error) {
	limit := defaultUserSearchLimit
	if raw, exists := ctx.GetQuery("limit"); exists {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("invalid limit")
		}
		limit = parsed
	}
	if limit > maxUserSearchLimit {
		limit = maxUserSearchLimit
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

func escapeUserSearchLike(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

func searchUsersFromDB(viewerID uint, query string, limit, offset int) (userConnectionPageResponse, error) {
	if global.Db == nil {
		return userConnectionPageResponse{}, errors.New("database is not initialized")
	}

	escapedQuery := escapeUserSearchLike(query)
	prefixPattern := escapedQuery + "%"
	containsPattern := "%" + escapedQuery + "%"
	rankSQL := `CASE
		WHEN LOWER(candidate.username) = LOWER(?) THEN 0
		WHEN LOWER(candidate.display_name) = LOWER(?) THEN 1
		WHEN candidate.username ILIKE ? ESCAPE '\' THEN 2
		WHEN candidate.display_name ILIKE ? ESCAPE '\' THEN 3
		WHEN candidate.username ILIKE ? ESCAPE '\' THEN 4
		WHEN candidate.display_name ILIKE ? ESCAPE '\' THEN 5
		ELSE 6
	END`
	orderBy := clause.OrderBy{Expression: clause.Expr{SQL: rankSQL + ", LOWER(candidate.username) ASC, candidate.id ASC", Vars: []any{query, query, prefixPattern, prefixPattern, containsPattern, containsPattern}}}
	queryDB := global.Db.Table("users AS candidate").
		Select(`candidate.id AS user_id, candidate.username AS username, candidate.display_name AS display_name, candidate.bio AS bio, candidate.avatar_url AS avatar_url, candidate.created_at AS user_created_at, viewer_follow.id AS viewer_follow_id`).
		Joins("LEFT JOIN user_follows AS viewer_follow ON viewer_follow.follower_id = ? AND viewer_follow.following_id = candidate.id", viewerID).
		Where("candidate.deleted_at IS NULL").
		Where("(candidate.username ILIKE ? ESCAPE '\\' OR candidate.display_name ILIKE ? ESCAPE '\\')", containsPattern, containsPattern).
		Order(orderBy).
		Limit(limit + 1).
		Offset(offset)

	var rows []userSearchQueryRow
	if err := queryDB.Scan(&rows).Error; err != nil {
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

func SearchUsers(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	rawQuery, exists := ctx.GetQuery("q")
	if !exists {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "search query is required"})
		return
	}
	query, err := normalizeUserSearchQuery(rawQuery)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, offset, err := parseUserSearchPagination(ctx)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, err := searchUsers(viewerID, query, limit, offset)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if page.Items == nil {
		page.Items = []userConnectionResponse{}
	}
	ctx.JSON(http.StatusOK, page)
}

func GetUserPosts(ctx *gin.Context) {
	id, err := parsePublicUserID(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	limit, cursor, err := parsePostPageQuery(ctx)
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

	now := time.Now().UTC()
	query := global.Db.
		Model(&models.Post{}).
		Where("posts.author_id = ? AND posts.reply_to_post_id IS NULL", id).
		Scopes(func(tx *gorm.DB) *gorm.DB { return publicPostScope(tx, now) })
	if cursor != nil {
		query = query.Where(
			"(posts.created_at < ?) OR (posts.created_at = ? AND posts.id < ?)",
			cursor.PublishedAt,
			cursor.PublishedAt,
			cursor.ID,
		)
	}
	posts, err := loadPostResponses(query.Order("posts.created_at DESC, posts.id DESC").Limit(limit + 1))
	if err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	page, err := buildPostPageResponse(posts, limit)
	if err != nil {
		writeUserAPIError(ctx, err)
		return
	}
	if page.Items == nil {
		page.Items = make([]postResponse, 0)
	}
	ctx.JSON(http.StatusOK, page)
}

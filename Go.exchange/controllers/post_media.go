package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const maxPostMediaCount = 4

type createPostMediaRequest struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type validatedPostMedia struct {
	MediaType string
	PublicURL string
	ObjectKey string
}

type postMediaResponse struct {
	Type     string `json:"type"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}

var (
	errInvalidPostMedia            = errors.New("invalid post media")
	errPostMediaObjectUnavailable  = errors.New("post media object is unavailable")
	errPostMediaStorageUnavailable = errors.New("post media storage is unavailable")
)

// parsePostMediaObjectURL accepts only the canonical URL returned by the
// post-media upload endpoint. It deliberately parses path segments instead
// of using a prefix check so user 1234 cannot match user 123.
func parsePostMediaObjectURL(viewerID uint, rawURL string) (string, error) {
	if viewerID == 0 || rawURL == "" || strings.ContainsAny(rawURL, "\r\n") || strings.ContainsAny(rawURL, "?#") {
		return "", errInvalidPostMedia
	}
	parts := strings.Split(rawURL, "/")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "api" || parts[2] != "files" || parts[3] != "post-media" {
		return "", errInvalidPostMedia
	}
	ownerID, err := strconv.ParseUint(parts[4], 10, 64)
	if err != nil || ownerID != uint64(viewerID) || parts[4] == "" || strconv.FormatUint(ownerID, 10) != parts[4] {
		return "", errInvalidPostMedia
	}

	filename := parts[5]
	dot := strings.LastIndexByte(filename, '.')
	if dot <= 0 || dot == len(filename)-1 {
		return "", errInvalidPostMedia
	}
	base, extension := filename[:dot], filename[dot:]
	parsedUUID, err := uuid.Parse(base)
	if err != nil || parsedUUID.String() != base {
		return "", errInvalidPostMedia
	}
	switch extension {
	case ".jpg", ".png", ".webp":
	default:
		return "", errInvalidPostMedia
	}

	objectKey := strings.Join(parts[3:], "/")
	if postFileURL(objectKey) != rawURL {
		return "", errInvalidPostMedia
	}
	return objectKey, nil
}

func validatePostMediaRequests(ctx context.Context, viewerID uint, items []createPostMediaRequest) ([]validatedPostMedia, error) {
	if len(items) > maxPostMediaCount {
		return nil, errInvalidPostMedia
	}
	validated := make([]validatedPostMedia, 0, len(items))
	seenURLs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Type != "image" {
			return nil, errInvalidPostMedia
		}
		if _, exists := seenURLs[item.URL]; exists {
			return nil, errInvalidPostMedia
		}
		seenURLs[item.URL] = struct{}{}
		objectKey, err := parsePostMediaObjectURL(viewerID, item.URL)
		if err != nil {
			return nil, err
		}
		if err := statStoredObject(ctx, objectKey); err != nil {
			if errors.Is(err, errPostMediaObjectUnavailable) {
				return nil, err
			}
			if errors.Is(err, errPostMediaStorageUnavailable) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %v", errPostMediaStorageUnavailable, err)
		}
		validated = append(validated, validatedPostMedia{MediaType: item.Type, PublicURL: item.URL, ObjectKey: objectKey})
	}
	return validated, nil
}

func postMediaResponsesFromValidated(items []validatedPostMedia) []postMediaResponse {
	responses := make([]postMediaResponse, 0, len(items))
	for position, item := range items {
		responses = append(responses, postMediaResponse{Type: item.MediaType, URL: item.PublicURL, Position: position})
	}
	return responses
}

var loadPostMediaByPostIDs = loadPostMediaByPostIDsFromDB

func loadPostMediaByPostIDsFromDB(db *gorm.DB, postIDs []uint) (map[uint][]postMediaResponse, error) {
	mediaByPostID := make(map[uint][]postMediaResponse)
	uniqueIDs := make([]uint, 0, len(postIDs))
	seenIDs := make(map[uint]struct{}, len(postIDs))
	for _, postID := range postIDs {
		if postID == 0 {
			continue
		}
		if _, exists := seenIDs[postID]; exists {
			continue
		}
		seenIDs[postID] = struct{}{}
		uniqueIDs = append(uniqueIDs, postID)
		mediaByPostID[postID] = make([]postMediaResponse, 0)
	}
	if len(uniqueIDs) == 0 {
		return mediaByPostID, nil
	}
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	var rows []models.PostMedia
	if err := db.Where("post_id IN ?", uniqueIDs).Order("post_id ASC, position ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		mediaByPostID[row.PostID] = append(mediaByPostID[row.PostID], postMediaResponse{
			Type: row.MediaType, URL: row.URL, Position: row.Position,
		})
	}
	return mediaByPostID, nil
}

func ensurePostResponseMedia(response *postResponse) {
	if response != nil && response.Media == nil {
		response.Media = make([]postMediaResponse, 0)
	}
}

func hydratePostResponseMediaFromDB(db *gorm.DB, response *postResponse) error {
	if response == nil {
		return nil
	}
	mediaByPostID, err := loadPostMediaByPostIDs(db, []uint{response.ID})
	if err != nil {
		return err
	}
	response.Media = mediaByPostID[response.ID]
	ensurePostResponseMedia(response)
	return nil
}

func hydratePostResponsesMediaFromDB(db *gorm.DB, responses []postResponse) error {
	postIDs := make([]uint, 0, len(responses))
	for index := range responses {
		postIDs = append(postIDs, responses[index].ID)
	}
	mediaByPostID, err := loadPostMediaByPostIDs(db, postIDs)
	if err != nil {
		return err
	}
	for index := range responses {
		responses[index].Media = mediaByPostID[responses[index].ID]
		ensurePostResponseMedia(&responses[index])
	}
	return nil
}

package controllers

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"Go.exchange/models"
	"Go.exchange/postmedia"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	maxPostMediaCount = 4
)

type createPostMediaRequest struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type validatedPostMedia struct {
	MediaID   string
	MediaType string
	PublicURL string
	LargeURL  string
	Width     int
	Height    int
}

type postMediaResponse struct {
	Type     string `json:"type"`
	URL      string `json:"url"`
	LargeURL string `json:"large_url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Position int    `json:"position"`
}

var errInvalidPostMedia = errors.New("invalid post media")

type parsedUserPostMedia struct {
	MediaID string
}

// parsePostMediaObjectURL accepts only the canonical Medium URL returned by
// the V1 post-media upload endpoint. It deliberately parses path segments
// instead of using a prefix check so user 1234 cannot match user 123.
func parsePostMediaObjectURL(viewerID uint, rawURL string) (parsedUserPostMedia, error) {
	if viewerID == 0 || rawURL == "" || strings.ContainsAny(rawURL, "\r\n") || strings.ContainsAny(rawURL, "?#") {
		return parsedUserPostMedia{}, errInvalidPostMedia
	}
	parts := strings.Split(rawURL, "/")
	if len(parts) != 9 || parts[0] != "" || parts[1] != "api" || parts[2] != "files" || parts[3] != "post-media" || parts[4] != "users" || parts[5] != "v1" {
		return parsedUserPostMedia{}, errInvalidPostMedia
	}
	ownerID, err := parseCanonicalPostMediaOwnerID(parts[6])
	if err != nil || ownerID != uint64(viewerID) {
		return parsedUserPostMedia{}, errInvalidPostMedia
	}
	mediaID := parts[7]
	if !isCanonicalPostMediaUUID(mediaID) {
		return parsedUserPostMedia{}, errInvalidPostMedia
	}
	if parts[8] != "medium.jpg" && parts[8] != "medium.png" {
		return parsedUserPostMedia{}, errInvalidPostMedia
	}

	mediumObjectKey := strings.Join(parts[3:], "/")
	if postmedia.PublicURL(mediumObjectKey) != rawURL {
		return parsedUserPostMedia{}, errInvalidPostMedia
	}
	return parsedUserPostMedia{MediaID: mediaID}, nil
}

func parseCanonicalPostMediaOwnerID(value string) (uint64, error) {
	ownerID, err := strconv.ParseUint(value, 10, 64)
	if err != nil || ownerID == 0 || strconv.FormatUint(ownerID, 10) != value {
		return 0, errInvalidPostMedia
	}
	return ownerID, nil
}

func isCanonicalPostMediaUUID(value string) bool {
	parsed, err := uuidParse(value)
	return err == nil && parsed == value
}

func uuidParse(value string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func validatePostMediaRequests(_ context.Context, viewerID uint, items []createPostMediaRequest) ([]validatedPostMedia, error) {
	if len(items) > maxPostMediaCount {
		return nil, errInvalidPostMedia
	}
	validated := make([]validatedPostMedia, 0, len(items))
	seenURLs := make(map[string]struct{}, len(items))
	seenMediaIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.Type != "image" {
			return nil, errInvalidPostMedia
		}
		if _, exists := seenURLs[item.URL]; exists {
			return nil, errInvalidPostMedia
		}
		seenURLs[item.URL] = struct{}{}
		parsed, err := parsePostMediaObjectURL(viewerID, item.URL)
		if err != nil {
			return nil, err
		}
		if _, exists := seenMediaIDs[parsed.MediaID]; exists {
			return nil, errInvalidPostMedia
		}
		seenMediaIDs[parsed.MediaID] = struct{}{}
		validated = append(validated, validatedPostMedia{
			MediaID: parsed.MediaID, MediaType: item.Type, PublicURL: item.URL,
		})
	}
	return validated, nil
}

func postMediaResponsesFromValidated(items []validatedPostMedia) []postMediaResponse {
	responses := make([]postMediaResponse, 0, len(items))
	for position, item := range items {
		responses = append(responses, postMediaResponse{
			Type: item.MediaType, URL: item.PublicURL, LargeURL: item.LargeURL,
			Width: item.Width, Height: item.Height, Position: position,
		})
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
			Type: row.MediaType, URL: row.URL, LargeURL: row.LargeURL,
			Width: row.Width, Height: row.Height, Position: row.Position,
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

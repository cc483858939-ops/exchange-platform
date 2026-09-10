package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"Go.exchange/avatarimage"
	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/postmedia"
	"Go.exchange/postmediaimage"
	"Go.exchange/profileavatar"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

const (
	postMediaObjectPrefix     = postmedia.UserV1ObjectPrefix
	maxPostMediaImageSize     = postmediaimage.MaxSourceBytes
	profileAvatarObjectPrefix = "profile-avatars/"
	maxProfileAvatarImageSize = 2 << 20
)

type postMediaUploadResponse struct {
	MediaURL string `json:"media_url"`
}

type storedObjectInfo struct {
	Size        int64
	ContentType string
}

var putStoredObject = func(ctx context.Context, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
	if global.MinioClient == nil {
		return errors.New("storage is not initialized")
	}
	_, err := global.MinioClient.PutObject(ctx, config.StorageBucket(), objectKey, reader, objectSize, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

var getStoredObject = func(ctx context.Context, objectKey string) (*minio.Object, error) {
	if global.MinioClient == nil {
		return nil, errors.New("storage is not initialized")
	}
	return global.MinioClient.GetObject(ctx, config.StorageBucket(), objectKey, minio.GetObjectOptions{})
}

// readStoredObject is the bounded internal object-reader seam used to load a
// user upload manifest. Stat happens before the bounded read so a malformed or
// unexpectedly large object cannot turn into an unbounded allocation.
var readStoredObject = func(ctx context.Context, objectKey string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errInvalidPostMedia
	}
	object, err := getStoredObject(ctx, objectKey)
	if err != nil {
		return nil, classifyStoredObjectError(err)
	}
	defer object.Close()
	info, err := object.Stat()
	if err != nil {
		return nil, classifyStoredObjectError(err)
	}
	if info.Size < 0 || info.Size > maxBytes {
		return nil, errInvalidPostMedia
	}
	body, err := io.ReadAll(io.LimitReader(object, maxBytes+1))
	if err != nil {
		return nil, classifyStoredObjectError(err)
	}
	if int64(len(body)) > maxBytes {
		return nil, errInvalidPostMedia
	}
	return body, nil
}

var statStoredObject = func(ctx context.Context, objectKey string) error {
	if global.MinioClient == nil {
		return errPostMediaStorageUnavailable
	}
	_, err := global.MinioClient.StatObject(ctx, config.StorageBucket(), objectKey, minio.StatObjectOptions{})
	if err == nil {
		return nil
	}
	if isMissingStoredObjectError(err) {
		return errPostMediaObjectUnavailable
	}
	return fmt.Errorf("%w: %v", errPostMediaStorageUnavailable, err)
}

var statProfileAvatarObject = func(ctx context.Context, objectKey string) (storedObjectInfo, bool, error) {
	if global.MinioClient == nil {
		return storedObjectInfo{}, false, errors.New("storage is not initialized")
	}
	info, err := global.MinioClient.StatObject(ctx, config.StorageBucket(), objectKey, minio.StatObjectOptions{})
	if err != nil {
		if isMissingStoredObjectError(err) {
			return storedObjectInfo{}, false, nil
		}
		return storedObjectInfo{}, false, fmt.Errorf("stat profile avatar object: %w", err)
	}
	return storedObjectInfo{Size: info.Size, ContentType: info.ContentType}, true, nil
}

func UploadPostMedia(ctx *gin.Context) {
	// Uploaded objects are intentionally retained when a later create-post
	// request fails; orphan cleanup is outside this phase and a later retry may
	// still reference the returned URL.
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	fileHeader, err := ctx.FormFile("image")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		return
	}
	if fileHeader.Size > maxPostMediaImageSize {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 5MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to open image file"})
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, int64(postmediaimage.MaxSourceBytes)+1))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image file"})
		return
	}
	if len(body) == 0 || len(body) > postmediaimage.MaxSourceBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 5MB"})
		return
	}
	processed, err := postmediaimage.Process(body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "only jpeg, png, or webp images are supported"})
		return
	}

	mediaID := uuid.NewString()
	paths, err := postmedia.BuildUserV1ObjectPaths(viewerID, mediaID, processed.OriginalExtension, processed.Medium.Extension)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build media object key"})
		return
	}
	requestContext := ctx.Request.Context()
	if err := putStoredObject(requestContext, paths.OriginalObjectKey, bytes.NewReader(body), int64(len(body)), processed.OriginalContentType); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	if err := putStoredObject(requestContext, paths.MediumObjectKey, bytes.NewReader(processed.Medium.Body), int64(len(processed.Medium.Body)), processed.Medium.ContentType); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	if err := putStoredObject(requestContext, paths.LargeObjectKey, bytes.NewReader(processed.Large.Body), int64(len(processed.Large.Body)), processed.Large.ContentType); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	manifestBody, err := json.Marshal(postmedia.Manifest{
		Version:           1,
		OwnerID:           viewerID,
		MediaID:           mediaID,
		OriginalObjectKey: paths.OriginalObjectKey,
		Medium: postmedia.VariantManifest{
			ObjectKey: paths.MediumObjectKey, ContentType: processed.Medium.ContentType,
			Width: processed.Medium.Width, Height: processed.Medium.Height,
		},
		Large: postmedia.VariantManifest{
			ObjectKey: paths.LargeObjectKey, ContentType: processed.Large.ContentType,
			Width: processed.Large.Width, Height: processed.Large.Height,
		},
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	if err := putStoredObject(requestContext, paths.ManifestObjectKey, bytes.NewReader(manifestBody), int64(len(manifestBody)), "application/json"); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}

	ctx.JSON(http.StatusOK, postMediaUploadResponse{
		MediaURL: postmedia.PublicURL(paths.MediumObjectKey),
	})
}

func UploadProfileAvatar(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	fileHeader, err := ctx.FormFile("image")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > maxProfileAvatarImageSize {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 2MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to open image file"})
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, int64(avatarimage.MaxSourceBytes)+1))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image file"})
		return
	}
	if len(body) == 0 || len(body) > avatarimage.MaxSourceBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 2MB"})
		return
	}

	derivative, err := avatarimage.Optimize(body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "only jpeg, png, or webp images are supported"})
		return
	}
	objectKey, err := profileavatar.BuildUserV1ObjectKey(viewerID, derivative.ContentHash, derivative.Extension)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build avatar object key"})
		return
	}
	info, exists, err := statProfileAvatarObject(ctx.Request.Context(), objectKey)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "avatar storage is unavailable"})
		return
	}
	if !exists || info.Size != int64(len(derivative.Body)) || info.ContentType != derivative.ContentType {
		if err := putStoredObject(ctx.Request.Context(), objectKey, bytes.NewReader(derivative.Body), int64(len(derivative.Body)), derivative.ContentType); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"avatar_url": postFileURL(objectKey)})
}
func GetFile(ctx *gin.Context) {
	objectKey := strings.TrimPrefix(ctx.Param("objectKey"), "/")
	if !isAllowedObjectKey(objectKey) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
		return
	}

	object, err := getStoredObject(ctx.Request.Context(), objectKey)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer object.Close()

	info, err := object.Stat()
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	ctx.Header("Cache-Control", fileCacheControl(objectKey))
	ctx.DataFromReader(http.StatusOK, info.Size, info.ContentType, object, nil)
}

func fileCacheControl(objectKey string) string {
	if postmedia.IsPublicObjectKey(objectKey) {
		return "public, max-age=31536000, immutable"
	}
	if strings.HasPrefix(objectKey, profileavatar.UserV1ObjectPrefix) || strings.HasPrefix(objectKey, profileavatar.DevDataV1ObjectPrefix) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=86400"
}

func postFileURL(objectKey string) string {
	return postmedia.PublicURL(objectKey)
}

func isAllowedObjectKey(objectKey string) bool {
	if strings.Contains(objectKey, "..") || strings.ContainsAny(objectKey, "\r\n") {
		return false
	}
	return postmedia.IsPublicObjectKey(objectKey) || strings.HasPrefix(objectKey, profileAvatarObjectPrefix)
}

func classifyStoredObjectError(err error) error {
	if isMissingStoredObjectError(err) {
		return errPostMediaObjectUnavailable
	}
	return fmt.Errorf("%w: %v", errPostMediaStorageUnavailable, err)
}

func isMissingStoredObjectError(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.StatusCode == http.StatusNotFound || response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound"
}

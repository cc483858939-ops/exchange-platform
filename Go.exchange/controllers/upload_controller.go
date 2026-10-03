package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"Go.exchange/avatarimage"
	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/postmedia"
	"Go.exchange/postmediaimage"
	"Go.exchange/postmediaupload"
	"Go.exchange/profileavatar"
	"Go.exchange/profilecover"
	"Go.exchange/profilecoverimage"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

const (
	postMediaObjectPrefix         = postmedia.UserV1ObjectPrefix
	maxPostMediaImageSize         = postmediaimage.MaxSourceBytes
	postMediaUploadCleanupTimeout = 5 * time.Second
	profileAvatarObjectPrefix     = "profile-avatars/"
	maxProfileAvatarImageSize     = 2 << 20
	// Allow MIME headers, boundaries, and small fields beyond the image budget.
	uploadMultipartOverhead = 64 << 10
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

var removeStoredObject = func(ctx context.Context, objectKey string) error {
	if global.MinioClient == nil {
		return errors.New("storage is not initialized")
	}
	if err := global.MinioClient.RemoveObject(ctx, config.StorageBucket(), objectKey, minio.RemoveObjectOptions{}); err != nil && !isMissingStoredObjectError(err) {
		return err
	}
	return nil
}

var createPendingPostMediaUpload = func(ctx context.Context, upload models.PostMediaUpload) error {
	return postmediaupload.CreateUploading(ctx, global.APIDb, upload)
}

var markPendingPostMediaUploadUploaded = func(ctx context.Context, mediaID string, ownerID uint, uploadedAt, cleanupAfter time.Time) error {
	return postmediaupload.MarkUploaded(ctx, global.APIDb, mediaID, ownerID, uploadedAt, cleanupAfter)
}

var deletePendingPostMediaUpload = func(ctx context.Context, mediaID string, ownerID uint) error {
	return postmediaupload.DeleteAfterObjectCleanup(ctx, global.APIDb, mediaID, ownerID)
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

var statProfileCoverObject = func(ctx context.Context, objectKey string) (storedObjectInfo, bool, error) {
	if global.MinioClient == nil {
		return storedObjectInfo{}, false, errors.New("storage is not initialized")
	}
	info, err := global.MinioClient.StatObject(ctx, config.StorageBucket(), objectKey, minio.StatObjectOptions{})
	if err != nil {
		if isMissingStoredObjectError(err) {
			return storedObjectInfo{}, false, nil
		}
		return storedObjectInfo{}, false, fmt.Errorf("stat profile cover object: %w", err)
	}
	return storedObjectInfo{Size: info.Size, ContentType: info.ContentType}, true, nil
}

func uploadImageFile(ctx *gin.Context, maxImageBytes int64) (*multipart.FileHeader, bool) {
	maxRequestBytes := maxImageBytes + uploadMultipartOverhead
	if ctx.Request.ContentLength > maxRequestBytes {
		ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "upload request is too large"})
		return nil, false
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxRequestBytes)
	form, err := ctx.MultipartForm()
	if err == nil {
		// Multipart parsing may stop at the closing boundary. Count trailing bytes
		// too, including when Content-Length is unknown (chunked uploads).
		_, err = io.Copy(io.Discard, ctx.Request.Body)
	}
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "upload request is too large"})
		} else {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		}
		return nil, false
	}
	files := form.File["image"]
	if len(files) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		return nil, false
	}
	return files[0], true
}

func UploadPostMedia(ctx *gin.Context) {
	// Every user upload is registered before object storage side effects begin.
	// A successful upload remains reusable until create-post consumes its lease.
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	fileHeader, ok := uploadImageFile(ctx, maxPostMediaImageSize)
	if !ok {
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
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build media manifest"})
		return
	}
	now := time.Now().UTC()
	if err := createPendingPostMediaUpload(ctx.Request.Context(), models.PostMediaUpload{
		MediaID: mediaID, OwnerID: viewerID, Status: postmediaupload.StatusUploading,
		OriginalObjectKey: paths.OriginalObjectKey, MediumObjectKey: paths.MediumObjectKey,
		LargeObjectKey: paths.LargeObjectKey, ManifestObjectKey: paths.ManifestObjectKey,
		MediumURL: postmedia.PublicURL(paths.MediumObjectKey), LargeURL: postmedia.PublicURL(paths.LargeObjectKey),
		Width: processed.Medium.Width, Height: processed.Medium.Height,
		CreatedAt: now, CleanupAfter: now.Add(postmediaupload.DefaultUploadTimeout),
	}); err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media upload registry is unavailable"})
		return
	}
	cleanupAfterFailure := func(cause error) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), postMediaUploadCleanupTimeout)
		defer cancel()
		if cleanupErr := cleanupPostMediaObjects(cleanupCtx, paths); cleanupErr != nil {
			log.Printf("[PostMediaUpload] best-effort object cleanup failed after %v: %v", cause, cleanupErr)
			return
		}
		if err := deletePendingPostMediaUpload(cleanupCtx, mediaID, viewerID); err != nil {
			log.Printf("[PostMediaUpload] failed to remove cleaned upload registry row %s: %v", mediaID, err)
		}
	}
	requestContext := ctx.Request.Context()
	if err := putStoredObject(requestContext, paths.OriginalObjectKey, bytes.NewReader(body), int64(len(body)), processed.OriginalContentType); err != nil {
		cleanupAfterFailure(err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	if err := putStoredObject(requestContext, paths.MediumObjectKey, bytes.NewReader(processed.Medium.Body), int64(len(processed.Medium.Body)), processed.Medium.ContentType); err != nil {
		cleanupAfterFailure(err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	if err := putStoredObject(requestContext, paths.LargeObjectKey, bytes.NewReader(processed.Large.Body), int64(len(processed.Large.Body)), processed.Large.ContentType); err != nil {
		cleanupAfterFailure(err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	if err := putStoredObject(requestContext, paths.ManifestObjectKey, bytes.NewReader(manifestBody), int64(len(manifestBody)), "application/json"); err != nil {
		cleanupAfterFailure(err)
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media storage is unavailable"})
		return
	}
	uploadedAt := time.Now().UTC()
	if err := markPendingPostMediaUploadUploaded(
		requestContext, mediaID, viewerID, uploadedAt,
		uploadedAt.Add(postmediaupload.DefaultGracePeriod),
	); err != nil {
		log.Printf(
			"[PostMediaUpload] upload payload retained after lifecycle finalization failure media_id=%s error_category=%s",
			mediaID,
			postMediaUploadFinalizationErrorCategory(err),
		)
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "media upload registry is unavailable"})
		return
	}

	ctx.JSON(http.StatusOK, postMediaUploadResponse{
		MediaURL: postmedia.PublicURL(paths.MediumObjectKey),
	})
}

func postMediaUploadFinalizationErrorCategory(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "request_cancelled"
	case errors.Is(err, context.DeadlineExceeded), isPostgresTimeoutError(err):
		return "database_timeout"
	case errors.Is(err, postmediaupload.ErrUploadUnavailable):
		return "lease_unavailable"
	default:
		return "database_error"
	}
}

func cleanupPostMediaObjects(ctx context.Context, paths postmedia.UserV1ObjectPaths) error {
	keys := []string{paths.OriginalObjectKey, paths.MediumObjectKey, paths.LargeObjectKey, paths.ManifestObjectKey}
	var cleanupErrors []error
	for _, objectKey := range keys {
		if err := removeStoredObject(ctx, objectKey); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove %s: %w", objectKey, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func UploadProfileAvatar(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	fileHeader, ok := uploadImageFile(ctx, maxProfileAvatarImageSize)
	if !ok {
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

func UploadProfileCover(ctx *gin.Context) {
	viewerID, ok := requireActiveProfileViewerID(ctx)
	if !ok {
		return
	}

	fileHeader, ok := uploadImageFile(ctx, profilecoverimage.MaxSourceBytes)
	if !ok {
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > profilecoverimage.MaxSourceBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 5MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to open image file"})
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, int64(profilecoverimage.MaxSourceBytes)+1))
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image file"})
		return
	}
	if len(body) == 0 || len(body) > profilecoverimage.MaxSourceBytes {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 5MB"})
		return
	}

	derivative, err := profilecoverimage.Optimize(body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "only jpeg, png, or webp images are supported"})
		return
	}
	objectKey, err := profilecover.BuildUserV1ObjectKey(viewerID, derivative.ContentHash, derivative.Extension)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build cover object key"})
		return
	}
	info, exists, err := statProfileCoverObject(ctx.Request.Context(), objectKey)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "cover storage is unavailable"})
		return
	}
	if !exists || info.Size != int64(len(derivative.Body)) || info.ContentType != derivative.ContentType {
		if err := putStoredObject(ctx.Request.Context(), objectKey, bytes.NewReader(derivative.Body), int64(len(derivative.Body)), derivative.ContentType); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "cover storage is unavailable"})
			return
		}
	}

	ctx.JSON(http.StatusOK, gin.H{"cover_image_url": profilecover.FilesURLPrefix + objectKey})
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
	if profilecover.IsPublicObjectKey(objectKey) {
		return "public, max-age=31536000, immutable"
	}
	if profilecover.IsDevDataPublicObjectKey(objectKey) {
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
	return postmedia.IsPublicObjectKey(objectKey) || profilecover.IsPublicObjectKey(objectKey) || profilecover.IsDevDataPublicObjectKey(objectKey) || strings.HasPrefix(objectKey, profileAvatarObjectPrefix)
}

func isMissingStoredObjectError(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.StatusCode == http.StatusNotFound || response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.Code == "NotFound"
}

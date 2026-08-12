package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"Go.exchange/config"
	"Go.exchange/global"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

const (
	articleCoverObjectPrefix  = "article-covers/"
	maxArticleCoverImageSize  = 5 << 20
	profileAvatarObjectPrefix = "profile-avatars/"
	maxProfileAvatarImageSize = 2 << 20
)

type articleCoverUploadResponse struct {
	CoverImageURL string `json:"cover_image_url"`
}

var putArticleCoverObject = func(ctx context.Context, objectKey string, reader io.Reader, objectSize int64, contentType string) error {
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

func UploadArticleCover(ctx *gin.Context) {
	fileHeader, err := ctx.FormFile("image")
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file is required"})
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > maxArticleCoverImageSize {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "image file must be between 1 byte and 5MB"})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to open image file"})
		return
	}
	defer file.Close()

	sniff := make([]byte, 512)
	n, err := file.Read(sniff)
	if err != nil && !errors.Is(err, io.EOF) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image file"})
		return
	}
	contentType, extension, ok := detectArticleCoverImageType(sniff[:n])
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "only jpeg, png, or webp images are supported"})
		return
	}

	objectKey := articleCoverObjectPrefix + uuid.NewString() + extension
	reader := io.MultiReader(bytes.NewReader(sniff[:n]), file)
	if err := putArticleCoverObject(ctx.Request.Context(), objectKey, reader, fileHeader.Size, contentType); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, articleCoverUploadResponse{
		CoverImageURL: articleFileURL(objectKey),
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

	sniff := make([]byte, 512)
	n, err := file.Read(sniff)
	if err != nil && !errors.Is(err, io.EOF) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read image file"})
		return
	}
	contentType, extension, ok := detectArticleCoverImageType(sniff[:n])
	if !ok {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "only jpeg, png, or webp images are supported"})
		return
	}

	objectKey := fmt.Sprintf("%s%d/%s%s", profileAvatarObjectPrefix, viewerID, uuid.NewString(), extension)
	reader := io.MultiReader(bytes.NewReader(sniff[:n]), file)
	if err := putArticleCoverObject(ctx.Request.Context(), objectKey, reader, fileHeader.Size, contentType); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"avatar_url": articleFileURL(objectKey)})
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

	ctx.Header("Cache-Control", "public, max-age=86400")
	ctx.DataFromReader(http.StatusOK, info.Size, info.ContentType, object, nil)
}

func detectArticleCoverImageType(sniff []byte) (string, string, bool) {
	if len(sniff) >= 12 && string(sniff[0:4]) == "RIFF" && string(sniff[8:12]) == "WEBP" {
		return "image/webp", ".webp", true
	}

	switch http.DetectContentType(sniff) {
	case "image/jpeg":
		return "image/jpeg", ".jpg", true
	case "image/png":
		return "image/png", ".png", true
	default:
		return "", "", false
	}
}

func articleFileURL(objectKey string) string {
	return fmt.Sprintf("/api/files/%s", objectKey)
}

func isAllowedObjectKey(objectKey string) bool {
	if strings.Contains(objectKey, "..") || strings.ContainsAny(objectKey, "\r\n") {
		return false
	}
	return strings.HasPrefix(objectKey, articleCoverObjectPrefix) || strings.HasPrefix(objectKey, profileAvatarObjectPrefix)
}

func isAllowedArticleObjectKey(objectKey string) bool {
	return strings.HasPrefix(objectKey, articleCoverObjectPrefix) && isAllowedObjectKey(objectKey)
}

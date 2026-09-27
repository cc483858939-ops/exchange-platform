package postmediagc

import (
	"context"
	"errors"
	"strings"

	"github.com/minio/minio-go/v7"
)

type MinIOObjectDeleter struct {
	client *minio.Client
	bucket string
}

func NewMinIOObjectDeleter(client *minio.Client, bucket string) (*MinIOObjectDeleter, error) {
	if client == nil {
		return nil, errors.New("MinIO client is not initialized")
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, errors.New("MinIO bucket is not configured")
	}
	return &MinIOObjectDeleter{client: client, bucket: bucket}, nil
}

func (deleter *MinIOObjectDeleter) DeleteObject(ctx context.Context, objectKey string) error {
	if deleter == nil || deleter.client == nil {
		return errors.New("MinIO object deleter is not initialized")
	}
	if ctx == nil {
		return errors.New("MinIO object deletion context is nil")
	}
	if strings.TrimSpace(objectKey) == "" {
		return errors.New("MinIO object key is empty")
	}
	err := deleter.client.RemoveObject(ctx, deleter.bucket, objectKey, minio.RemoveObjectOptions{})
	if isMissingObjectError(err) {
		return nil
	}
	return err
}

func isMissingObjectError(err error) bool {
	if err == nil {
		return false
	}
	response := minio.ToErrorResponse(err)
	return response.Code == "NoSuchKey" || response.Code == "NoSuchObject"
}

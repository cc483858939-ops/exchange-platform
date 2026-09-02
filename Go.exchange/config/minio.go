package config

import (
	"context"
	"fmt"
	"log"
	"time"

	"Go.exchange/global"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var storageBucketExists = func(ctx context.Context, client *minio.Client, bucket string) (bool, error) {
	return client.BucketExists(ctx, bucket)
}

var storageMakeBucket = func(ctx context.Context, client *minio.Client, bucket string) error {
	return client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
}

// NewStorageClient creates a MinIO client and makes sure the configured bucket
// exists. It returns errors to callers that can degrade gracefully, such as
// operator tooling; production startup continues to use initStorage below.
func NewStorageClient() (*minio.Client, error) {
	client, err := minio.New(StorageEndpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(StorageAccessKey(), StorageSecretKey(), ""),
		Secure: StorageUseSSL(),
	})
	if err != nil {
		return nil, fmt.Errorf("initialize MinIO client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bucket := StorageBucket()
	exists, err := storageBucketExists(ctx, client, bucket)
	if err != nil {
		return nil, fmt.Errorf("check MinIO bucket: %w", err)
	}
	if !exists {
		if err := storageMakeBucket(ctx, client, bucket); err != nil && !isBucketAlreadyExistsError(err) {
			return nil, fmt.Errorf("create MinIO bucket: %w", err)
		}
	}
	return client, nil
}

func initStorage() {
	client, err := NewStorageClient()
	if err != nil {
		log.Fatalf("Failed to initialize MinIO storage, got error:%v", err)
	}
	global.MinioClient = client
}

func isBucketAlreadyExistsError(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "BucketAlreadyOwnedByYou" || response.Code == "BucketAlreadyExists"
}

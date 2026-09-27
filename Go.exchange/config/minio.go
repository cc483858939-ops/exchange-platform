package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var storageBucketExists = func(ctx context.Context, client *minio.Client, bucket string) (bool, error) {
	return client.BucketExists(ctx, bucket)
}

var storageMakeBucket = func(ctx context.Context, client *minio.Client, bucket string) error {
	return client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
}

type ExistingBucketStorageOptions struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

// NewStorageClient creates a MinIO client and makes sure the configured bucket
// exists. The API composition root owns failure handling.
func NewStorageClient() (*minio.Client, error) {
	client, err := newStorageClient()
	if err != nil {
		return nil, err
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

// NewStorageClientForExistingBucketWithOptions creates a client from dedicated
// credentials without bucket probes or creation; provisioning owns the bucket.
func NewStorageClientForExistingBucketWithOptions(options ExistingBucketStorageOptions) (*minio.Client, error) {
	if strings.TrimSpace(options.Endpoint) == "" || strings.TrimSpace(options.AccessKey) == "" || strings.TrimSpace(options.SecretKey) == "" {
		return nil, errors.New("MinIO endpoint and dedicated access credentials are required")
	}
	return createStorageClient(options.Endpoint, options.AccessKey, options.SecretKey, options.UseSSL)
}

func newStorageClient() (*minio.Client, error) {
	return createStorageClient(StorageEndpoint(), StorageAccessKey(), StorageSecretKey(), StorageUseSSL())
}

func createStorageClient(endpoint, accessKey, secretKey string, secure bool) (*minio.Client, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: secure,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize MinIO client: %w", err)
	}

	return client, nil
}

func isBucketAlreadyExistsError(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "BucketAlreadyOwnedByYou" || response.Code == "BucketAlreadyExists"
}

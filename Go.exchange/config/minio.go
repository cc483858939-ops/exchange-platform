package config

import (
	"context"
	"log"
	"time"

	"Go.exchange/global"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func initStorage() {
	client, err := minio.New(StorageEndpoint(), &minio.Options{
		Creds:  credentials.NewStaticV4(StorageAccessKey(), StorageSecretKey(), ""),
		Secure: StorageUseSSL(),
	})
	if err != nil {
		log.Fatalf("Failed to initialize MinIO client, got error:%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bucket := StorageBucket()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		log.Fatalf("Failed to check MinIO bucket, got error:%v", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil && !isBucketAlreadyExistsError(err) {
			log.Fatalf("Failed to create MinIO bucket, got error:%v", err)
		}
	}

	global.MinioClient = client
}

func isBucketAlreadyExistsError(err error) bool {
	response := minio.ToErrorResponse(err)
	return response.Code == "BucketAlreadyOwnedByYou" || response.Code == "BucketAlreadyExists"
}

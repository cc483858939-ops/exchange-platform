package config

import (
	"context"
	"errors"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestNewStorageClientCreatesMissingBucketWithoutFatalPath(t *testing.T) {
	t.Setenv("MINIO_ENDPOINT", "127.0.0.1:9000")
	t.Setenv("MINIO_ACCESS_KEY", "unit-test-access")
	t.Setenv("MINIO_SECRET_KEY", "unit-test-secret")
	t.Setenv("MINIO_BUCKET", "unit-test-bucket")
	t.Setenv("MINIO_USE_SSL", "false")

	originalExists := storageBucketExists
	originalMake := storageMakeBucket
	t.Cleanup(func() {
		storageBucketExists = originalExists
		storageMakeBucket = originalMake
	})
	var madeBucket string
	storageBucketExists = func(_ context.Context, _ *minio.Client, bucket string) (bool, error) {
		if bucket != "unit-test-bucket" {
			t.Fatalf("bucket=%q", bucket)
		}
		return false, nil
	}
	storageMakeBucket = func(_ context.Context, _ *minio.Client, bucket string) error {
		madeBucket = bucket
		return nil
	}

	client, err := NewStorageClient()
	if err != nil {
		t.Fatalf("NewStorageClient: %v", err)
	}
	if client == nil || madeBucket != "unit-test-bucket" {
		t.Fatalf("client=%v madeBucket=%q", client != nil, madeBucket)
	}
}

func TestNewStorageClientReturnsBucketCheckError(t *testing.T) {
	t.Setenv("MINIO_ENDPOINT", "127.0.0.1:9000")
	t.Setenv("MINIO_ACCESS_KEY", "unit-test-access")
	t.Setenv("MINIO_SECRET_KEY", "unit-test-secret")
	t.Setenv("MINIO_BUCKET", "unit-test-bucket")
	t.Setenv("MINIO_USE_SSL", "false")

	originalExists := storageBucketExists
	t.Cleanup(func() { storageBucketExists = originalExists })
	wantErr := errors.New("bucket check failed")
	storageBucketExists = func(context.Context, *minio.Client, string) (bool, error) { return false, wantErr }

	client, err := NewStorageClient()
	if client != nil || !errors.Is(err, wantErr) {
		t.Fatalf("client=%v err=%v", client, err)
	}
}

func TestNewStorageClientForExistingBucketDoesNotProbeOrCreateBucket(t *testing.T) {
	originalExists := storageBucketExists
	originalMake := storageMakeBucket
	t.Cleanup(func() {
		storageBucketExists = originalExists
		storageMakeBucket = originalMake
	})
	called := false
	storageBucketExists = func(context.Context, *minio.Client, string) (bool, error) {
		called = true
		return false, nil
	}
	storageMakeBucket = func(context.Context, *minio.Client, string) error {
		called = true
		return nil
	}

	client, err := NewStorageClientForExistingBucketWithOptions(ExistingBucketStorageOptions{
		Endpoint: "127.0.0.1:9000", AccessKey: "dedicated-gc-access", SecretKey: "dedicated-gc-secret",
	})
	if err != nil {
		t.Fatalf("NewStorageClientForExistingBucketWithOptions: %v", err)
	}
	if client == nil || called {
		t.Fatalf("client=%v bucket privilege probes called=%t", client != nil, called)
	}
}

func TestNewStorageClientForExistingBucketRequiresDedicatedCredentials(t *testing.T) {
	if client, err := NewStorageClientForExistingBucketWithOptions(ExistingBucketStorageOptions{}); client != nil || err == nil {
		t.Fatalf("client=%v err=%v, want missing dedicated credentials error", client, err)
	}
}

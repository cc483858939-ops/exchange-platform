package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"Go.exchange/config"
	"Go.exchange/postmediagc"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	applicationConfig, err := config.Load()
	if err != nil {
		return fmt.Errorf("load Post media GC configuration: %w", err)
	}
	gcConfig := postmediagc.LoadConfigFromEnv()
	if err := gcConfig.Validate(); err != nil {
		return fmt.Errorf("validate Post media GC configuration: %w", err)
	}
	dsn, err := requiredEnvironment("POST_MEDIA_GC_DATABASE_DSN")
	if err != nil {
		return err
	}
	db, err := config.OpenMaintenanceDatabaseWithDSN(applicationConfig, dsn)
	if err != nil {
		return fmt.Errorf("open Post media GC database: %w", err)
	}
	defer func() {
		if err := config.CloseDatabase(db); err != nil {
			log.Printf("close Post media GC database: %v", err)
		}
	}()

	storageOptions, bucket, err := storageOptionsFromEnvironment()
	if err != nil {
		return err
	}
	client, err := config.NewStorageClientForExistingBucketWithOptions(storageOptions)
	if err != nil {
		return fmt.Errorf("initialize Post media GC storage: %w", err)
	}
	deleter, err := postmediagc.NewMinIOObjectDeleter(client, bucket)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runCtx, cancel := context.WithTimeout(ctx, gcConfig.RunTimeout)
	defer cancel()

	runner := postmediagc.NewRunner(postmediagc.NewGormStore(db), deleter, gcConfig)
	summary, runErr := runner.Run(runCtx)
	log.Printf("post-media-gc claimed=%d cleaned=%d retry_scheduled=%d stale_claims=%d duration_ms=%d",
		summary.Claimed, summary.Cleaned, summary.RetryScheduled, summary.StaleClaims, summary.Duration.Milliseconds())
	if errors.Is(runErr, postmediagc.ErrRowsRetryScheduled) {
		return fmt.Errorf("Post media GC completed with retryable storage failures: %w", runErr)
	}
	return runErr
}

func storageOptionsFromEnvironment() (config.ExistingBucketStorageOptions, string, error) {
	endpoint, err := requiredEnvironment("POST_MEDIA_GC_MINIO_ENDPOINT")
	if err != nil {
		return config.ExistingBucketStorageOptions{}, "", err
	}
	accessKey, err := requiredEnvironment("POST_MEDIA_GC_MINIO_ACCESS_KEY")
	if err != nil {
		return config.ExistingBucketStorageOptions{}, "", err
	}
	secretKey, err := requiredEnvironment("POST_MEDIA_GC_MINIO_SECRET_KEY")
	if err != nil {
		return config.ExistingBucketStorageOptions{}, "", err
	}
	bucket, err := requiredEnvironment("POST_MEDIA_GC_MINIO_BUCKET")
	if err != nil {
		return config.ExistingBucketStorageOptions{}, "", err
	}
	useSSLValue, err := requiredEnvironment("POST_MEDIA_GC_MINIO_USE_SSL")
	if err != nil {
		return config.ExistingBucketStorageOptions{}, "", err
	}
	useSSL, err := strconv.ParseBool(useSSLValue)
	if err != nil {
		return config.ExistingBucketStorageOptions{}, "", errors.New("POST_MEDIA_GC_MINIO_USE_SSL must be true or false")
	}
	return config.ExistingBucketStorageOptions{
		Endpoint: endpoint, AccessKey: accessKey, SecretKey: secretKey, UseSSL: useSSL,
	}, bucket, nil
}

func requiredEnvironment(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return value, nil
}

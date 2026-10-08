package controllers

import (
	"bytes"
	"context"
	"errors"
	"image"
	"net/http"
	"os"
	"strconv"
	"time"

	"Go.exchange/imagebudget"

	"github.com/gin-gonic/gin"
)

// Defaults bound mixed upload work; operators can tune the limits with measured
// peak memory/CPU. This gate covers avatar, cover and Post media together.
var uploadImageBudget = imagebudget.New(
	positiveImageBudgetEnv("IMAGE_PROCESSING_CONCURRENCY", 2),
	positiveImageBudgetEnv("IMAGE_PROCESSING_MAX_WAITERS", 8),
	int64(positiveImageBudgetEnv("IMAGE_PROCESSING_MAX_PIXELS", 25_000_000)),
	time.Duration(positiveImageBudgetEnv("IMAGE_PROCESSING_WAIT_SECONDS", 10))*time.Second,
)

func positiveImageBudgetEnv(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func processUploadedImage[T any](ctx context.Context, body []byte, maxPixels int64, process func(context.Context, []byte) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width) > maxPixels/int64(config.Height) {
		return zero, errors.New("unsafe image dimensions")
	}
	release, err := uploadImageBudget.Acquire(ctx, int64(config.Width)*int64(config.Height))
	if err != nil {
		return zero, err
	}
	defer release()
	return process(ctx, body)
}

func writeUploadImageError(ctx *gin.Context, err error, invalidMessage string) {
	if ctx.Request.Context().Err() != nil {
		return
	}
	if errors.Is(err, imagebudget.ErrBusy) {
		ctx.Header("Retry-After", "1")
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "image processing is busy; try again later"})
		return
	}
	ctx.JSON(http.StatusBadRequest, gin.H{"error": invalidMessage})
}

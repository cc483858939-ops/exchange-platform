package controllers

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"Go.exchange/avatarimage"
	"Go.exchange/imagebudget"
	"Go.exchange/postmediaimage"
	"Go.exchange/profilecoverimage"
	"github.com/gin-gonic/gin"
)

func TestCanceledUploadedImageNeverStartsCPUProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var calls atomic.Int64
	_, err := processUploadedImage(ctx, []byte("not even decoded"), 100, func(context.Context, []byte) (int, error) { calls.Add(1); return 1, nil })
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("err=%v work=%d", err, calls.Load())
	}
	if _, err := avatarimage.OptimizeContext(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := profilecoverimage.OptimizeContext(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := postmediaimage.ProcessContext(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestImageProcessingBudgetReleasedOnPipelineFailure(t *testing.T) {
	previous := uploadImageBudget
	uploadImageBudget = imagebudget.New(1, 1, 25_000_000, time.Second)
	t.Cleanup(func() { uploadImageBudget = previous })
	payload := profilePNGFixture(t)
	failure := errors.New("codec failure")
	_, err := processUploadedImage(t.Context(), payload, 25_000_000, func(context.Context, []byte) (int, error) { return 0, failure })
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	_, err = processUploadedImage(t.Context(), payload, 25_000_000, func(context.Context, []byte) (int, error) { return 1, nil })
	if err != nil {
		t.Fatalf("budget leaked after failure: %v", err)
	}
}

func TestUploadCanceledAfterMultipartReadDoesNotOptimizeOrStore(t *testing.T) {
	for _, endpoint := range uploadLimitEndpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			lifecycle, storageCalls := stubUploadLimitDependencies(t)
			body, contentType := uploadLimitBody(t, profilePNGFixture(t), "", 0, false)
			requestCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("user_id", uint(42))
			ctx.Request = httptest.NewRequest("POST", "/api/uploads/image", bytes.NewReader(body)).WithContext(requestCtx)
			ctx.Request.Header.Set("Content-Type", contentType)
			ctx.Request.Body = &uploadCountingBody{ReadCloser: ctx.Request.Body, onRead: cancel}
			endpoint.handler(ctx)
			if ctx.Request.MultipartForm != nil {
				ctx.Request.MultipartForm.RemoveAll()
			}
			if *storageCalls != 0 || lifecycle.createCalls != 0 {
				t.Fatalf("canceled upload performed storage/lease work: storage=%d lifecycle=%+v", *storageCalls, lifecycle)
			}
		})
	}
}

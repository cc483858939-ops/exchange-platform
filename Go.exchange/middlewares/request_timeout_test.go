package middlewares

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRequestTimeoutReturnsGatewayTimeout(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "20ms")
	t.Setenv("API_UPLOAD_REQUEST_TIMEOUT", "2s")
	router := gin.New()
	router.Use(RequestTimeout())
	router.GET("/api/slow", func(ctx *gin.Context) {
		<-ctx.Request.Context().Done()
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/slow", nil))

	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusGatewayTimeout)
	}
}

func TestRequestTimeoutPreservesUploadBudget(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "20ms")
	t.Setenv("API_UPLOAD_REQUEST_TIMEOUT", "2s")
	router := gin.New()
	router.Use(RequestTimeout())
	router.POST("/api/uploads/profile-cover", func(ctx *gin.Context) {
		deadline, ok := ctx.Request.Context().Deadline()
		if !ok || time.Until(deadline) <= 20*time.Millisecond {
			ctx.Status(http.StatusInternalServerError)
			return
		}
		time.Sleep(40 * time.Millisecond)
		ctx.Status(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/uploads/profile-cover", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRequestTimeoutDoesNotWriteAfterClientCancellation(t *testing.T) {
	t.Setenv("API_REQUEST_TIMEOUT", "1s")
	router := gin.New()
	router.Use(RequestTimeout())
	router.GET("/api/canceled", func(ctx *gin.Context) {
		<-ctx.Request.Context().Done()
	})
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/api/canceled", nil).WithContext(requestCtx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	if recorder.Code == http.StatusGatewayTimeout || recorder.Body.Len() != 0 {
		t.Fatalf("canceled request wrote response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

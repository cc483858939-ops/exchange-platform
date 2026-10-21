package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUserLookupInternalErrorIsSanitized(t *testing.T) {
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })

	previousLoader := loadPublicUserByExactUsername
	loadPublicUserByExactUsername = func(_ context.Context, _ string) (publicUserSummaryResponse, error) {
		return publicUserSummaryResponse{}, errors.New("postgres://admin:TEST_A2_PASSWORD@db.internal:5432/app SELECT * FROM private_users TEST_A2_SQL")
	}
	t.Cleanup(func() { loadPublicUserByExactUsername = previousLoader })

	engine := gin.New()
	engine.GET("/api/users/by-username/:username", GetUserByExactUsername)
	request := httptest.NewRequest(http.MethodGet, "/api/users/by-username/alice", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	body := response.Body.String()
	for _, sensitive := range []string{"TEST_A2_PASSWORD", "db.internal", "TEST_A2_SQL", "private_users"} {
		if strings.Contains(body, sensitive) {
			t.Fatalf("response leaked internal marker %q: %s", sensitive, body)
		}
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want %d; body=%s", response.Code, http.StatusInternalServerError, body)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("response is missing X-Request-ID")
	}
	if !strings.Contains(body, `"code":"INTERNAL_ERROR"`) || !strings.Contains(body, `"error":"Internal server error"`) || !strings.Contains(body, `"request_id":"`+response.Header().Get("X-Request-ID")+`"`) {
		t.Fatalf("response does not match the safe API error contract: %s", body)
	}
}

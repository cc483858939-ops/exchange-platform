package middlewares

import (
	"Go.exchange/auth"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type verifierStub struct {
	claims *auth.AccessClaims
	err    error
}

func (v verifierStub) VerifyAccess(_ string) (*auth.AccessClaims, error) {
	return v.claims, v.err
}

func TestAuthMiddlewareRequiresStrictBearerHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, header := range []string{"", "token", "Basic token", "Bearer", "Bearer one two"} {
		recorder := httptest.NewRecorder()
		ctx, router := gin.CreateTestContext(recorder)
		router.Use(AuthMiddleware(verifierStub{err: errors.New("must not be called")}))
		router.GET("/protected", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", header)
		ctx.Request = request
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("header=%q status=%d", header, recorder.Code)
		}
	}
}

func TestAuthMiddlewareSetsUserAndSessionIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.Use(AuthMiddleware(verifierStub{claims: &auth.AccessClaims{
		SessionID:        "session-1",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "42"},
	}}))
	router.GET("/protected", func(ctx *gin.Context) {
		userID, _ := ctx.Get("user_id")
		sessionID, _ := ctx.Get("session_id")
		if userID != uint(42) || sessionID != "session-1" {
			t.Fatalf("user_id=%v session_id=%v", userID, sessionID)
		}
		ctx.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "bearer signed-token")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

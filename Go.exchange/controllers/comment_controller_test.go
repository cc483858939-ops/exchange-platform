package controllers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newCommentUnitContext(method, target, id string, body *bytes.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	if body == nil {
		ctx.Request = httptest.NewRequest(method, target, nil)
	} else {
		ctx.Request = httptest.NewRequest(method, target, body)
	}
	ctx.Params = gin.Params{{Key: "id", Value: id}}
	return ctx, recorder
}

func TestNormalizeCommentContent(t *testing.T) {
	valid, err := normalizeCommentContent("  你好 👋  ")
	if err != nil || valid != "你好 👋" {
		t.Fatalf("normalized=%q err=%v", valid, err)
	}
	for _, raw := range []string{"", " \t\n "} {
		if _, err := normalizeCommentContent(raw); err == nil {
			t.Fatalf("expected empty content %q to fail", raw)
		}
	}
	if _, err := normalizeCommentContent(strings.Repeat("你", maxCommentContentRunes)); err != nil {
		t.Fatalf("1000 Unicode characters should be accepted: %v", err)
	}
	if _, err := normalizeCommentContent(strings.Repeat("你", maxCommentContentRunes+1)); err == nil {
		t.Fatal("1001 Unicode characters should be rejected")
	}
}

func TestCommentCursorRoundTripAndValidation(t *testing.T) {
	original := commentCursor{CreatedAt: time.Date(2026, 8, 9, 12, 34, 56, 789123456, time.UTC), ID: 42}
	encoded, err := encodeCommentCursor(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCommentCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != original.ID || !decoded.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("decoded=%#v original=%#v", decoded, original)
	}

	invalidJSON := base64.RawURLEncoding.EncodeToString([]byte("{"))
	zeroID, err := json.Marshal(commentCursor{CreatedAt: original.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "not-a-cursor", invalidJSON, base64.RawURLEncoding.EncodeToString(zeroID)} {
		if _, err := decodeCommentCursor(raw); err == nil {
			t.Fatalf("cursor %q should be rejected", raw)
		}
	}
}

func TestParseCommentLimitAndID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		target string
		want   int
		bad    bool
	}{
		{target: "/api/articles/1/comments", want: defaultCommentLimit},
		{target: "/api/articles/1/comments?limit=2", want: 2},
		{target: "/api/articles/1/comments?limit=999", want: maxCommentLimit},
		{target: "/api/articles/1/comments?limit=0", bad: true},
		{target: "/api/articles/1/comments?limit=invalid", bad: true},
	} {
		ctx, _ := newCommentUnitContext(http.MethodGet, test.target, "7", nil)
		got, err := parseCommentLimit(ctx)
		if test.bad {
			if err == nil {
				t.Fatalf("target %s should fail", test.target)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Fatalf("target %s limit=%d err=%v", test.target, got, err)
		}
	}

	ctx, recorder := newCommentUnitContext(http.MethodDelete, "/api/comments/9", "9", nil)
	if id, ok := commentIDFromContext(ctx); !ok || id != 9 || recorder.Code != http.StatusOK {
		t.Fatalf("valid id=%d ok=%t status=%d", id, ok, recorder.Code)
	}
	for _, raw := range []string{"0", "-1", "bad"} {
		ctx, recorder = newCommentUnitContext(http.MethodDelete, "/api/comments/"+raw, raw, nil)
		if _, ok := commentIDFromContext(ctx); ok || recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid id %q status=%d", raw, recorder.Code)
		}
	}
}

func TestCreateArticleCommentRejectsOversizedRequestBeforeDatabaseLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"content":"` + strings.Repeat("a", commentRequestMaxBytes) + `"}`)
	ctx, recorder := newCommentUnitContext(http.MethodPost, "/api/articles/7/comments", "7", bytes.NewReader(body))
	ctx.Set("user_id", uint(3))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateArticleComment(ctx)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

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

func newReplyUnitContext(method, target, id string, body *bytes.Reader) (*gin.Context, *httptest.ResponseRecorder) {
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

func TestNormalizeReplyContent(t *testing.T) {
	valid, err := normalizeReplyContent("  你好 👋  ")
	if err != nil || valid != "你好 👋" {
		t.Fatalf("normalized=%q err=%v", valid, err)
	}
	for _, raw := range []string{"", " \t\n "} {
		if _, err := normalizeReplyContent(raw); err == nil {
			t.Fatalf("expected empty content %q to fail", raw)
		}
	}
	if _, err := normalizeReplyContent(strings.Repeat("你", maxReplyContentRunes)); err != nil {
		t.Fatalf("1000 Unicode characters should be accepted: %v", err)
	}
	if _, err := normalizeReplyContent(strings.Repeat("你", maxReplyContentRunes+1)); err == nil {
		t.Fatal("1001 Unicode characters should be rejected")
	}
}

func TestReplyCursorRoundTripAndValidation(t *testing.T) {
	original := replyCursor{CreatedAt: time.Date(2026, 8, 9, 12, 34, 56, 789123456, time.UTC), ID: 42}
	encoded, err := encodeReplyCursor(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeReplyCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != original.ID || !decoded.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("decoded=%#v original=%#v", decoded, original)
	}

	invalidJSON := base64.RawURLEncoding.EncodeToString([]byte("{"))
	zeroID, err := json.Marshal(replyCursor{CreatedAt: original.CreatedAt})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "not-a-cursor", invalidJSON, base64.RawURLEncoding.EncodeToString(zeroID)} {
		if _, err := decodeReplyCursor(raw); err == nil {
			t.Fatalf("cursor %q should be rejected", raw)
		}
	}
}

func TestParseReplyLimitAndID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		target string
		want   int
		bad    bool
	}{
		{target: "/api/posts/1/replies", want: defaultReplyLimit},
		{target: "/api/posts/1/replies?limit=2", want: 2},
		{target: "/api/posts/1/replies?limit=999", want: maxReplyLimit},
		{target: "/api/posts/1/replies?limit=0", bad: true},
		{target: "/api/posts/1/replies?limit=invalid", bad: true},
	} {
		ctx, _ := newReplyUnitContext(http.MethodGet, test.target, "7", nil)
		got, err := parseReplyLimit(ctx)
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

	ctx, recorder := newReplyUnitContext(http.MethodDelete, "/api/posts/9", "9", nil)
	if id, ok := replyIDFromContext(ctx); !ok || id != 9 || recorder.Code != http.StatusOK {
		t.Fatalf("valid id=%d ok=%t status=%d", id, ok, recorder.Code)
	}
	for _, raw := range []string{"0", "-1", "bad"} {
		ctx, recorder = newReplyUnitContext(http.MethodDelete, "/api/posts/"+raw, raw, nil)
		if _, ok := replyIDFromContext(ctx); ok || recorder.Code != http.StatusBadRequest {
			t.Fatalf("invalid id %q status=%d", raw, recorder.Code)
		}
	}
}

func TestCreatePostReplyRejectsOversizedRequestBeforeDatabaseLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"content":"` + strings.Repeat("a", replyRequestMaxBytes) + `"}`)
	ctx, recorder := newReplyUnitContext(http.MethodPost, "/api/posts/7/replies", "7", bytes.NewReader(body))
	ctx.Set("user_id", uint(3))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreatePostReply(ctx)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

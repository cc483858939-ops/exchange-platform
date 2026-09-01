package controllers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

}

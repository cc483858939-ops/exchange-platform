package controllers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
)

type countedRequestBody struct {
	io.Reader
	read int
}

func (body *countedRequestBody) Read(p []byte) (int, error) {
	n, err := body.Reader.Read(p)
	body.read += n
	return n, err
}

func (*countedRequestBody) Close() error { return nil }

func TestPostStateHandlersBoundRequestBodyBeforeLoading(t *testing.T) {
	handlers := map[string]gin.HandlerFunc{
		"engagement": GetPostEngagementStates, "like": GetPostLikeStates,
		"repost": GetPostRepostStates, "bookmark": GetPostBookmarkStates,
	}
	valid := `{"post_ids":[42]}`
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name, body string
				length     int64
				status     int
			}{
				{"known-large", `{"post_ids":[42],"ignored":"` + strings.Repeat("x", 1<<20) + `"}`, 1 << 20, 413},
				{"chunked-large-field", `{"post_ids":[42],"ignored":"` + strings.Repeat("x", 1<<20) + `"}`, -1, 413},
				{"chunked-large-tail", valid + strings.Repeat(" ", 1<<20), -1, 413},
				{"at-limit", valid + strings.Repeat(" ", postStateRequestMaxBytes-len(valid)), -1, 401},
				{"over-limit", valid + strings.Repeat(" ", postStateRequestMaxBytes-len(valid)+1), -1, 413},
				{"invalid-count", `{"post_ids":[]}`, -1, 400},
				{"multiple-values", valid + `{}`, -1, 400},
				{"malformed", `{"post_ids":`, -1, 400},
			} {
				t.Run(test.name, func(t *testing.T) {
					writer := httptest.NewRecorder()
					ctx, _ := gin.CreateTestContext(writer)
					body := &countedRequestBody{Reader: strings.NewReader(test.body)}
					ctx.Request = httptest.NewRequest(http.MethodPost, "/states", nil)
					ctx.Request.Body = body
					ctx.Request.ContentLength = test.length
					handler(ctx)
					if writer.Code != test.status {
						t.Fatalf("status=%d want=%d body=%s", writer.Code, test.status, writer.Body.String())
					}
					if body.read > postStateRequestMaxBytes+1 {
						t.Fatalf("read %d bytes beyond budget", body.read)
					}
					if test.name == "known-large" && body.read != 0 {
						t.Fatalf("known oversized body read=%d", body.read)
					}
				})
			}
		})
	}
}

func TestProfilePatchPreservesBodyLimitErrors(t *testing.T) {
	for _, body := range []string{
		`{"bio":"` + strings.Repeat("x", 1<<20) + `"}`,
		`{"bio":"valid"}` + strings.Repeat(" ", 1<<20),
	} {
		counted := &countedRequestBody{Reader: strings.NewReader(body)}
		_, err := decodeUserProfilePatch(http.MaxBytesReader(httptest.NewRecorder(), counted, profilePatchMaxBytes), 42)
		if !isRequestBodyTooLarge(err) || counted.read > profilePatchMaxBytes+1 {
			t.Fatalf("err=%v bytes read=%d", err, counted.read)
		}
	}
}

func TestProfilePatchBoundsOwnerBodyAndRejectsOtherOwnerBeforeReading(t *testing.T) {
	previous := loadActiveProfileViewer
	loadActiveProfileViewer = func(context.Context, uint) (models.User, error) { return models.User{}, nil }
	t.Cleanup(func() { loadActiveProfileViewer = previous })
	for _, test := range []struct {
		target string
		length int64
		status int
	}{
		{"42", -1, http.StatusRequestEntityTooLarge},
		{"42", 1 << 20, http.StatusRequestEntityTooLarge},
		{"43", -1, http.StatusForbidden},
	} {
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		body := &countedRequestBody{Reader: strings.NewReader(`{"bio":"` + strings.Repeat("x", 1<<20) + `"}`)}
		ctx.Request = httptest.NewRequest(http.MethodPatch, "/users/"+test.target, nil)
		ctx.Request.Body = body
		ctx.Request.ContentLength = test.length
		ctx.Params = gin.Params{{Key: "id", Value: test.target}}
		ctx.Set("user_id", uint(42))
		UpdateUserProfile(ctx)
		if writer.Code != test.status || body.read > profilePatchMaxBytes+1 {
			t.Fatalf("status=%d read=%d", writer.Code, body.read)
		}
		if (test.status == http.StatusForbidden || test.length > 0) && body.read != 0 {
			t.Fatalf("body read before owner/length rejection: %d", body.read)
		}
	}
}

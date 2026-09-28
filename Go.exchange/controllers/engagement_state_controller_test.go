package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func newEngagementStateTestContext(body string, viewerID *uint) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts/engagement-states", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if viewerID != nil {
		ctx.Set("user_id", *viewerID)
	}
	return ctx, recorder
}

func restoreEngagementStateLoaders(t *testing.T) {
	t.Helper()
	likeLoader := loadPostLikeStates
	repostLoader := loadPostRepostStates
	bookmarkLoader := loadPostBookmarkStates
	t.Cleanup(func() {
		loadPostLikeStates = likeLoader
		loadPostRepostStates = repostLoader
		loadPostBookmarkStates = bookmarkLoader
	})
}

func readyEngagementStateResults(postIDs []uint) (postLikeStatesLoadResult, postRepostStatesLoadResult, postBookmarkStatesLoadResult) {
	likes := make(map[uint]postLikeStateResult, len(postIDs))
	reposts := make(map[uint]postRepostStateResult, len(postIDs))
	bookmarks := make(map[uint]postBookmarkStateResult, len(postIDs))
	for _, id := range postIDs {
		likes[id] = postLikeStateResult{Likes: int64(id), Liked: id%2 == 0}
		reposts[id] = postRepostStateResult{Reposts: int64(id + 1), Reposted: id%2 == 1}
		bookmarks[id] = postBookmarkStateResult{PostID: id, Bookmarked: id%2 == 0}
	}
	return postLikeStatesLoadResult{States: likes}, postRepostStatesLoadResult{States: reposts}, postBookmarkStatesLoadResult{States: bookmarks}
}

func installReadyEngagementStateLoaders() {
	loadPostLikeStates = func(_ context.Context, _ uint, ids []uint) (postLikeStatesLoadResult, error) {
		likes, _, _ := readyEngagementStateResults(ids)
		return likes, nil
	}
	loadPostRepostStates = func(_ context.Context, _ uint, ids []uint) (postRepostStatesLoadResult, error) {
		_, reposts, _ := readyEngagementStateResults(ids)
		return reposts, nil
	}
	loadPostBookmarkStates = func(_ context.Context, _ uint, ids []uint) (postBookmarkStatesLoadResult, error) {
		_, _, bookmarks := readyEngagementStateResults(ids)
		return bookmarks, nil
	}
}

func TestGetPostEngagementStatesValidatesAuthAndInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	invalidBodies := []string{
		`{}`,
		`{"post_ids":[]}`,
		`{"post_ids":[0]}`,
		`{"post_ids":[-1]}`,
		`{"post_ids":["7"]}`,
		`{"post_ids":[7}`,
	}
	tooMany := make([]string, maxPostLikeStateIDs+1)
	for i := range tooMany {
		tooMany[i] = "7"
	}
	invalidBodies = append(invalidBodies, `{"post_ids":[`+strings.Join(tooMany, ",")+`]}`)
	for _, body := range invalidBodies {
		t.Run("invalid/"+body, func(t *testing.T) {
			restoreEngagementStateLoaders(t)
			loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
				t.Fatal("loader called for invalid request")
				return postLikeStatesLoadResult{}, nil
			}
			viewerID := uint(5)
			ctx, recorder := newEngagementStateTestContext(body, &viewerID)
			GetPostEngagementStates(ctx)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
			}
		})
	}

	t.Run("unauthenticated", func(t *testing.T) {
		restoreEngagementStateLoaders(t)
		loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
			t.Fatal("loader called without authenticated user")
			return postLikeStatesLoadResult{}, nil
		}
		ctx, recorder := newEngagementStateTestContext(`{"post_ids":[7]}`, nil)
		GetPostEngagementStates(ctx)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d response=%s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestGetPostEngagementStatesDeduplicatesPreservesOrderAndReturnsIndependentStates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreEngagementStateLoaders(t)
	viewerID := uint(11)
	installReadyEngagementStateLoaders()
	loadPostLikeStates = func(_ context.Context, userID uint, ids []uint) (postLikeStatesLoadResult, error) {
		if userID != viewerID || !equalUintSlices(ids, []uint{9, 4, 7}) {
			t.Fatalf("like loader args user=%d ids=%v", userID, ids)
		}
		return postLikeStatesLoadResult{
			States: map[uint]postLikeStateResult{
				9: {Likes: 0, Liked: false},
				7: {Likes: 12, Liked: true},
			},
			Unavailable: []uint{4},
		}, nil
	}
	ctx, recorder := newEngagementStateTestContext(`{"post_ids":[9,9,4,7]}`, &viewerID)
	GetPostEngagementStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d response=%s", recorder.Code, recorder.Body.String())
	}
	var response engagementStatesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 3 || response.Items[0].PostID != 9 || response.Items[1].PostID != 4 || response.Items[2].PostID != 7 {
		t.Fatalf("items order/dedupe=%+v", response.Items)
	}
	if response.Items[0].Like.Status != "ready" || response.Items[0].Like.Likes == nil || *response.Items[0].Like.Likes != 0 || response.Items[0].Like.Liked == nil || *response.Items[0].Like.Liked {
		t.Fatalf("false/zero ready like fields were not preserved: %+v", response.Items[0].Like)
	}
	if response.Items[1].Like.Status != "unavailable" || response.Items[1].Like.Likes != nil || response.Items[1].Like.Liked != nil {
		t.Fatalf("per-post unavailable like=%+v", response.Items[1].Like)
	}
	if response.Items[1].Repost.Status != "ready" || response.Items[1].Repost.Reposts == nil || *response.Items[1].Repost.Reposts != 5 || response.Items[1].Repost.Reposted == nil || *response.Items[1].Repost.Reposted {
		t.Fatalf("repost state=%+v", response.Items[1].Repost)
	}
	if response.Items[1].Bookmark.Status != "ready" || response.Items[1].Bookmark.Bookmarked == nil || !*response.Items[1].Bookmark.Bookmarked {
		t.Fatalf("bookmark state=%+v", response.Items[1].Bookmark)
	}
}

func TestGetPostEngagementStatesIsolatesSubsystemFailuresAndMissingStates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viewerID := uint(11)
	cases := []struct {
		name         string
		failLike     bool
		failRepost   bool
		failBookmark bool
		wantStatus   int
	}{
		{name: "like failure", failLike: true},
		{name: "repost failure", failRepost: true},
		{name: "bookmark failure", failBookmark: true},
		{name: "two failures", failLike: true, failRepost: true},
		{name: "all failures", failLike: true, failRepost: true, failBookmark: true, wantStatus: http.StatusServiceUnavailable},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			restoreEngagementStateLoaders(t)
			installReadyEngagementStateLoaders()
			if testCase.failLike {
				loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
					return postLikeStatesLoadResult{}, errors.New("like internal secret")
				}
			}
			if testCase.failRepost {
				loadPostRepostStates = func(context.Context, uint, []uint) (postRepostStatesLoadResult, error) {
					return postRepostStatesLoadResult{}, errors.New("repost internal secret")
				}
			}
			if testCase.failBookmark {
				loadPostBookmarkStates = func(context.Context, uint, []uint) (postBookmarkStatesLoadResult, error) {
					return postBookmarkStatesLoadResult{}, errors.New("bookmark internal secret")
				}
			}
			ctx, recorder := newEngagementStateTestContext(`{"post_ids":[7,8]}`, &viewerID)
			GetPostEngagementStates(ctx)
			wantStatus := testCase.wantStatus
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			if recorder.Code != wantStatus {
				t.Fatalf("status=%d want=%d response=%s", recorder.Code, wantStatus, recorder.Body.String())
			}
			if testCase.wantStatus == http.StatusServiceUnavailable {
				for _, secret := range []string{"like internal secret", "repost internal secret", "bookmark internal secret"} {
					if strings.Contains(recorder.Body.String(), secret) {
						t.Fatalf("leaked internal error %q in %s", secret, recorder.Body.String())
					}
				}
				return
			}
			var response engagementStatesResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Items) != 2 {
				t.Fatalf("items=%+v", response.Items)
			}
			for _, item := range response.Items {
				wantLike := "ready"
				if testCase.failLike {
					wantLike = "unavailable"
				}
				wantRepost := "ready"
				if testCase.failRepost {
					wantRepost = "unavailable"
				}
				wantBookmark := "ready"
				if testCase.failBookmark {
					wantBookmark = "unavailable"
				}
				if item.Like.Status != wantLike || item.Repost.Status != wantRepost || item.Bookmark.Status != wantBookmark {
					t.Fatalf("independent statuses=%+v", item)
				}
			}
		})
	}

	t.Run("missing state map entry falls back to unavailable", func(t *testing.T) {
		restoreEngagementStateLoaders(t)
		installReadyEngagementStateLoaders()
		loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
			return postLikeStatesLoadResult{States: map[uint]postLikeStateResult{7: {Likes: 3, Liked: true}}}, nil
		}
		ctx, recorder := newEngagementStateTestContext(`{"post_ids":[7,8]}`, &viewerID)
		GetPostEngagementStates(ctx)
		var response engagementStatesResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Items[1].Like.Status != "unavailable" || response.Items[1].Like.Likes != nil {
			t.Fatalf("missing state produced a partial item: %+v", response.Items[1].Like)
		}
	})
}

func TestGetPostEngagementStatesRunsLoadersConcurrently(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreEngagementStateLoaders(t)
	viewerID := uint(11)
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	loadPostLikeStates = func(context.Context, uint, []uint) (postLikeStatesLoadResult, error) {
		started <- struct{}{}
		<-release
		likes, _, _ := readyEngagementStateResults([]uint{7})
		return likes, nil
	}
	loadPostRepostStates = func(context.Context, uint, []uint) (postRepostStatesLoadResult, error) {
		started <- struct{}{}
		<-release
		_, reposts, _ := readyEngagementStateResults([]uint{7})
		return reposts, nil
	}
	loadPostBookmarkStates = func(context.Context, uint, []uint) (postBookmarkStatesLoadResult, error) {
		started <- struct{}{}
		<-release
		_, _, bookmarks := readyEngagementStateResults([]uint{7})
		return bookmarks, nil
	}
	ctx, recorder := newEngagementStateTestContext(`{"post_ids":[7]}`, &viewerID)
	done := make(chan struct{})
	go func() {
		GetPostEngagementStates(ctx)
		close(done)
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			<-done
			t.Fatalf("only %d loaders started before release; loaders are not concurrent", i)
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("engagement handler did not finish after loaders were released")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d response=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetPostEngagementStatesPassesRequestContextToEachLoader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreEngagementStateLoaders(t)
	viewerID := uint(11)
	type contextKey string
	const key contextKey = "engagement-request"
	request := httptest.NewRequest(http.MethodPost, "/api/posts/engagement-states", strings.NewReader(`{"post_ids":[7]}`))
	request = request.WithContext(context.WithValue(request.Context(), key, "request-context"))
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	ctx.Set("user_id", viewerID)
	checked := make(chan error, 3)
	checkContext := func(got context.Context) {
		if got.Value(key) != "request-context" {
			checked <- fmt.Errorf("request context value = %v", got.Value(key))
			return
		}
		checked <- nil
	}
	loadPostLikeStates = func(ctx context.Context, _ uint, ids []uint) (postLikeStatesLoadResult, error) {
		checkContext(ctx)
		likes, _, _ := readyEngagementStateResults(ids)
		return likes, nil
	}
	loadPostRepostStates = func(ctx context.Context, _ uint, ids []uint) (postRepostStatesLoadResult, error) {
		checkContext(ctx)
		_, reposts, _ := readyEngagementStateResults(ids)
		return reposts, nil
	}
	loadPostBookmarkStates = func(ctx context.Context, _ uint, ids []uint) (postBookmarkStatesLoadResult, error) {
		checkContext(ctx)
		_, _, bookmarks := readyEngagementStateResults(ids)
		return bookmarks, nil
	}
	GetPostEngagementStates(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d response=%s", recorder.Code, recorder.Body.String())
	}
	for i := 0; i < 3; i++ {
		if err := <-checked; err != nil {
			t.Fatal(err)
		}
	}
}

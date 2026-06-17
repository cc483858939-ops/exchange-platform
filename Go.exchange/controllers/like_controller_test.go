package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestLikeArticleRecordsBehaviorOnlyWhenChangedToLiked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)

	setArticleLikedState = func(userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
		if userID != 11 || articleID != 7 || !liked {
			t.Fatalf("unexpected like input: userID=%d articleID=%d liked=%v", userID, articleID, liked)
		}
		return articleLikeMutationResult{Likes: 4, Liked: true, ChangedToLiked: true}, nil
	}

	recorded := false
	recordArticleBehavior = func(userID uint, articleID uint, action string) error {
		recorded = userID == 11 && articleID == 7 && action == ArticleBehaviorActionLike
		return nil
	}
	archiveArticleBehavior = func(uint, uint, string) error {
		t.Fatal("did not expect archive on like")
		return nil
	}
	articleBehaviorLogError = func(*gin.Context, string, error) {}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))

	LikeArticle(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !recorded {
		t.Fatal("expected like behavior to be recorded")
	}

	payload := decodeLikePayload(t, recorder)
	if payload.Likes != 4 || !payload.Liked {
		t.Fatalf("unexpected response payload: %#v", payload)
	}
}

func TestLikeArticleDoesNotRecordBehaviorWhenAlreadyLiked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)

	setArticleLikedState = func(userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
		if userID != 11 || articleID != 7 || !liked {
			t.Fatalf("unexpected like input: userID=%d articleID=%d liked=%v", userID, articleID, liked)
		}
		return articleLikeMutationResult{Likes: 4, Liked: true}, nil
	}
	recordArticleBehavior = func(uint, uint, string) error {
		t.Fatal("did not expect behavior record for idempotent like")
		return nil
	}
	archiveArticleBehavior = func(uint, uint, string) error {
		t.Fatal("did not expect archive on like")
		return nil
	}
	articleBehaviorLogError = func(*gin.Context, string, error) {}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))

	LikeArticle(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeLikePayload(t, recorder)
	if payload.Likes != 4 || !payload.Liked {
		t.Fatalf("unexpected response payload: %#v", payload)
	}
}

func TestUnlikeArticleArchivesBehaviorOnlyWhenChangedToUnliked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)

	setArticleLikedState = func(userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
		if userID != 11 || articleID != 7 || liked {
			t.Fatalf("unexpected unlike input: userID=%d articleID=%d liked=%v", userID, articleID, liked)
		}
		return articleLikeMutationResult{Likes: 3, Liked: false, ChangedToUnliked: true}, nil
	}

	recordArticleBehavior = func(uint, uint, string) error {
		t.Fatal("did not expect record on unlike")
		return nil
	}
	archived := false
	archiveArticleBehavior = func(userID uint, articleID uint, action string) error {
		archived = userID == 11 && articleID == 7 && action == ArticleBehaviorActionLike
		return nil
	}
	articleBehaviorLogError = func(*gin.Context, string, error) {}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))

	UnlikeArticle(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !archived {
		t.Fatal("expected like behavior to be archived")
	}

	payload := decodeLikePayload(t, recorder)
	if payload.Likes != 3 || payload.Liked {
		t.Fatalf("unexpected response payload: %#v", payload)
	}
}

func TestUnlikeArticleDoesNotArchiveBehaviorWhenAlreadyUnliked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)

	setArticleLikedState = func(userID uint, articleID uint, liked bool) (articleLikeMutationResult, error) {
		if userID != 11 || articleID != 7 || liked {
			t.Fatalf("unexpected unlike input: userID=%d articleID=%d liked=%v", userID, articleID, liked)
		}
		return articleLikeMutationResult{Likes: 3, Liked: false}, nil
	}
	recordArticleBehavior = func(uint, uint, string) error {
		t.Fatal("did not expect record on unlike")
		return nil
	}
	archiveArticleBehavior = func(uint, uint, string) error {
		t.Fatal("did not expect archive for idempotent unlike")
		return nil
	}
	articleBehaviorLogError = func(*gin.Context, string, error) {}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))

	UnlikeArticle(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", recorder.Code, recorder.Body.String())
	}
	payload := decodeLikePayload(t, recorder)
	if payload.Likes != 3 || payload.Liked {
		t.Fatalf("unexpected response payload: %#v", payload)
	}
}

func TestGetArticleLikesReturnsLikedState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restoreLikeControllerMocks(t)

	loadArticleLikeState = func(userID uint, articleID uint) (articleLikeStateResult, error) {
		if userID != 11 || articleID != 7 {
			t.Fatalf("unexpected load input: userID=%d articleID=%d", userID, articleID)
		}
		return articleLikeStateResult{Likes: 9, Liked: true}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "7"}}
	ctx.Set("user_id", uint(11))

	GetArticleLikes(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d body=%s", recorder.Code, recorder.Body.String())
	}

	payload := decodeLikePayload(t, recorder)
	if payload.Likes != 9 || !payload.Liked {
		t.Fatalf("unexpected response payload: %#v", payload)
	}
}

func decodeLikePayload(t *testing.T, recorder *httptest.ResponseRecorder) struct {
	Likes int64 `json:"likes"`
	Liked bool  `json:"liked"`
} {
	t.Helper()

	var payload struct {
		Likes int64 `json:"likes"`
		Liked bool  `json:"liked"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func restoreLikeControllerMocks(t *testing.T) {
	t.Helper()

	originalSetArticleLikedState := setArticleLikedState
	originalLoadArticleLikeState := loadArticleLikeState
	originalRecordArticleBehavior := recordArticleBehavior
	originalArchiveArticleBehavior := archiveArticleBehavior
	originalArticleBehaviorLogError := articleBehaviorLogError

	t.Cleanup(func() {
		setArticleLikedState = originalSetArticleLikedState
		loadArticleLikeState = originalLoadArticleLikeState
		recordArticleBehavior = originalRecordArticleBehavior
		archiveArticleBehavior = originalArchiveArticleBehavior
		articleBehaviorLogError = originalArticleBehaviorLogError
	})
}

package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
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

func TestSetArticleLikedStateInTxInsertsReactionAndAppliesPositiveDelta(t *testing.T) {
	restoreLikeControllerMocks(t)

	loadArticleLikeBaseline = func(tx *gorm.DB, articleID uint) (models.Article, error) {
		if tx != nil || articleID != 7 {
			t.Fatalf("unexpected baseline input: tx=%v articleID=%d", tx, articleID)
		}
		return models.Article{LikeCount: 10}, nil
	}
	insertArticleLikeReaction = func(tx *gorm.DB, userID uint, articleID uint) (bool, error) {
		if tx != nil || userID != 11 || articleID != 7 {
			t.Fatalf("unexpected insert input: tx=%v userID=%d articleID=%d", tx, userID, articleID)
		}
		return true, nil
	}
	deleteArticleLikeReaction = func(*gorm.DB, uint, uint) (bool, error) {
		t.Fatal("did not expect delete for like")
		return false, nil
	}
	loadArticleLikeCount = func(uint) (int64, error) {
		t.Fatal("did not expect count reload when state changed")
		return 0, nil
	}
	applyArticleLikeDelta = func(articleID uint, delta int64, baselineLikes int64) (int64, error) {
		if articleID != 7 || delta != 1 || baselineLikes != 10 {
			t.Fatalf("unexpected delta input: articleID=%d delta=%d baseline=%d", articleID, delta, baselineLikes)
		}
		return 11, nil
	}

	result, err := setArticleLikedStateInTx(nil, 11, 7, true)
	if err != nil {
		t.Fatalf("set liked state: %v", err)
	}
	if result.Likes != 11 || !result.Liked || !result.ChangedToLiked || result.ChangedToUnliked {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestSetArticleLikedStateInTxIdempotentLikeDoesNotApplyDelta(t *testing.T) {
	restoreLikeControllerMocks(t)

	loadArticleLikeBaseline = func(*gorm.DB, uint) (models.Article, error) {
		return models.Article{LikeCount: 10}, nil
	}
	insertArticleLikeReaction = func(*gorm.DB, uint, uint) (bool, error) {
		return false, nil
	}
	applyArticleLikeDelta = func(uint, int64, int64) (int64, error) {
		t.Fatal("did not expect delta for idempotent like")
		return 0, nil
	}
	loadArticleLikeCount = func(articleID uint) (int64, error) {
		if articleID != 7 {
			t.Fatalf("unexpected count articleID=%d", articleID)
		}
		return 10, nil
	}

	result, err := setArticleLikedStateInTx(nil, 11, 7, true)
	if err != nil {
		t.Fatalf("set liked state: %v", err)
	}
	if result.Likes != 10 || !result.Liked || result.ChangedToLiked || result.ChangedToUnliked {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestSetArticleLikedStateInTxDeletesReactionAndAppliesNegativeDelta(t *testing.T) {
	restoreLikeControllerMocks(t)

	loadArticleLikeBaseline = func(*gorm.DB, uint) (models.Article, error) {
		return models.Article{LikeCount: 10}, nil
	}
	insertArticleLikeReaction = func(*gorm.DB, uint, uint) (bool, error) {
		t.Fatal("did not expect insert for unlike")
		return false, nil
	}
	deleteArticleLikeReaction = func(tx *gorm.DB, userID uint, articleID uint) (bool, error) {
		if tx != nil || userID != 11 || articleID != 7 {
			t.Fatalf("unexpected delete input: tx=%v userID=%d articleID=%d", tx, userID, articleID)
		}
		return true, nil
	}
	loadArticleLikeCount = func(uint) (int64, error) {
		t.Fatal("did not expect count reload when state changed")
		return 0, nil
	}
	applyArticleLikeDelta = func(articleID uint, delta int64, baselineLikes int64) (int64, error) {
		if articleID != 7 || delta != -1 || baselineLikes != 10 {
			t.Fatalf("unexpected delta input: articleID=%d delta=%d baseline=%d", articleID, delta, baselineLikes)
		}
		return 9, nil
	}

	result, err := setArticleLikedStateInTx(nil, 11, 7, false)
	if err != nil {
		t.Fatalf("set liked state: %v", err)
	}
	if result.Likes != 9 || result.Liked || result.ChangedToLiked || !result.ChangedToUnliked {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestSetArticleLikedStateInTxIdempotentUnlikeDoesNotApplyDelta(t *testing.T) {
	restoreLikeControllerMocks(t)

	loadArticleLikeBaseline = func(*gorm.DB, uint) (models.Article, error) {
		return models.Article{LikeCount: 10}, nil
	}
	deleteArticleLikeReaction = func(*gorm.DB, uint, uint) (bool, error) {
		return false, nil
	}
	applyArticleLikeDelta = func(uint, int64, int64) (int64, error) {
		t.Fatal("did not expect delta for idempotent unlike")
		return 0, nil
	}
	loadArticleLikeCount = func(articleID uint) (int64, error) {
		if articleID != 7 {
			t.Fatalf("unexpected count articleID=%d", articleID)
		}
		return 10, nil
	}

	result, err := setArticleLikedStateInTx(nil, 11, 7, false)
	if err != nil {
		t.Fatalf("set liked state: %v", err)
	}
	if result.Likes != 10 || result.Liked || result.ChangedToLiked || result.ChangedToUnliked {
		t.Fatalf("unexpected result: %#v", result)
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
	originalApplyArticleLikeDelta := applyArticleLikeDelta
	originalLoadArticleLikeBaseline := loadArticleLikeBaseline
	originalInsertArticleLikeReaction := insertArticleLikeReaction
	originalDeleteArticleLikeReaction := deleteArticleLikeReaction
	originalLoadArticleLikeCount := loadArticleLikeCount
	originalRecordArticleBehavior := recordArticleBehavior
	originalArchiveArticleBehavior := archiveArticleBehavior
	originalArticleBehaviorLogError := articleBehaviorLogError

	t.Cleanup(func() {
		setArticleLikedState = originalSetArticleLikedState
		loadArticleLikeState = originalLoadArticleLikeState
		applyArticleLikeDelta = originalApplyArticleLikeDelta
		loadArticleLikeBaseline = originalLoadArticleLikeBaseline
		insertArticleLikeReaction = originalInsertArticleLikeReaction
		deleteArticleLikeReaction = originalDeleteArticleLikeReaction
		loadArticleLikeCount = originalLoadArticleLikeCount
		recordArticleBehavior = originalRecordArticleBehavior
		archiveArticleBehavior = originalArchiveArticleBehavior
		articleBehaviorLogError = originalArticleBehaviorLogError
	})
}

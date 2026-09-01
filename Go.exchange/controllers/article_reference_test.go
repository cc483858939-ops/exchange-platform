package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestLoadPostReferencePropagatesDatabaseInitializationError(t *testing.T) {
	postID := uint(42)
	reference, err := loadPostReferenceFromDB(nil, &postID, time.Now().UTC())
	if err == nil {
		t.Fatal("expected database initialization error")
	}
	if reference != nil {
		t.Fatalf("reference=%#v want nil on infrastructure error", reference)
	}

	response := postResponse{QuotePostID: &postID}
	if err := hydratePostResponseReferencesFromDB(nil, &response, time.Now().UTC()); err == nil {
		t.Fatal("expected reference hydration to propagate database initialization error")
	}
}

func TestSelectedRecommendationResponsesPropagatesReferenceHydrationError(t *testing.T) {
	previousDB := global.Db
	global.Db = nil
	t.Cleanup(func() { global.Db = previousDB })

	quoteID := uint(99)
	_, err := selectedRecommendationResponses([]selectedRecommendation{{
		Post: models.Post{
			Model:       gorm.Model{ID: 1},
			AuthorID:    7,
			Author:      models.User{Model: gorm.Model{ID: 7}},
			QuotePostID: &quoteID,
		},
	}})
	if err == nil {
		t.Fatal("expected recommendation response hydration error")
	}
}

func TestGetPostByIDReturnsServerErrorForReferenceHydrationFailure(t *testing.T) {
	previousDB := global.Db
	previousCache := loadPostDetailCache
	global.Db = nil
	loadPostDetailCache = func(string, func() (postResponse, error)) (postResponse, error) {
		quoteID := uint(99)
		publishedAt := time.Now().UTC()
		return postResponse{
			ID:          1,
			PublishedAt: &publishedAt,
			QuotePostID: &quoteID,
			Visibility:  "public",
		}, nil
	}
	t.Cleanup(func() {
		global.Db = previousDB
		loadPostDetailCache = previousCache
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	GetPostByID(ctx)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s want 500", recorder.Code, recorder.Body.String())
	}
}

func TestCreatePostReturnsServerErrorForReferenceHydrationFailure(t *testing.T) {
	previousDB := global.Db
	previousAuthorLoader := loadPostAuthorForCreate
	previousPersist := persistPostGraphFn
	global.Db = nil
	loadPostAuthorForCreate = func(id uint) (publicAuthorResponse, error) {
		return publicAuthorResponse{ID: id, Username: "author"}, nil
	}
	persistPostGraphFn = func(post *models.Post, _ **models.PostArticle, userID uint, content string, _ createPostRequest, now time.Time) error {
		quoteID := uint(99)
		*post = models.Post{
			Model:       gorm.Model{ID: 1, CreatedAt: now, UpdatedAt: now},
			AuthorID:    userID,
			Content:     content,
			QuotePostID: &quoteID,
			Visibility:  "public",
		}
		return nil
	}
	t.Cleanup(func() {
		global.Db = previousDB
		loadPostAuthorForCreate = previousAuthorLoader
		persistPostGraphFn = previousPersist
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", strings.NewReader(`{"content":"quote"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	createPost(ctx, nil)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s want 500", recorder.Code, recorder.Body.String())
	}
}

func TestLoadPostReferenceClassifiesStatesIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiredAt := now.Add(-time.Minute)

	deletedPost := models.Post{
		Model:      gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID:   fixture.Author.ID,
		Content:    "deleted reference content",
		Visibility: "public",
	}
	unavailablePost := models.Post{
		Model:      gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID:   fixture.Author.ID,
		Content:    "expired reference content",
		Visibility: "public",
	}
	if err := db.Create(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&unavailablePost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostArticle{
		PostID: unavailablePost.ID, Title: "expired reference", Preview: "expired preview",
		PublicationState: consts.PostPublicationStatePublished, PublishedAt: &now, ExpiredAt: &expiredAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	ids := []uint{deletedPost.ID, unavailablePost.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", ids).Delete(&models.PostArticle{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Post{})
	})

	activeID := fixture.Article.ID
	active, err := loadPostReferenceFromDB(db, &activeID, now)
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.State != postReferenceStateActive || active.Author == nil || active.Content == "" {
		t.Fatalf("active reference=%#v", active)
	}

	deleted, err := loadPostReferenceFromDB(db, &deletedPost.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	assertPostReferenceStateWithoutContent(t, deleted, postReferenceStateDeleted)

	unavailable, err := loadPostReferenceFromDB(db, &unavailablePost.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	assertPostReferenceStateWithoutContent(t, unavailable, postReferenceStateUnavailable)

	missingID := unavailablePost.ID + 1000000
	missing, err := loadPostReferenceFromDB(db, &missingID, now)
	if err != nil {
		t.Fatal(err)
	}
	assertPostReferenceStateWithoutContent(t, missing, postReferenceStateUnavailable)
}

func assertPostReferenceStateWithoutContent(t *testing.T, reference *postReferenceResponse, want postReferenceState) {
	t.Helper()
	if reference == nil || reference.ID == 0 || reference.State != want {
		t.Fatalf("reference=%#v want state=%q", reference, want)
	}
	payload, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"author", "content", "published_at", "article"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("state=%q leaked %s: %s", want, forbidden, payload)
		}
	}
}

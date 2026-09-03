package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	persistPostGraphFn = func(post *models.Post, userID uint, content string, _ createPostRequest, _ []validatedPostMedia, now time.Time) error {
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

func TestLoadPostReferenceUsesExactWireUnionIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)

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
	futurePost := models.Post{
		Model:    gorm.Model{CreatedAt: now.Add(time.Hour), UpdatedAt: now.Add(time.Hour)},
		AuthorID: fixture.Author.ID, Content: "future reference content", Visibility: "public",
	}
	if err := db.Create(&futurePost).Error; err != nil {
		t.Fatal(err)
	}
	inactiveAuthor := models.User{Username: "inactive-reference-author-" + strings.ReplaceAll(t.Name(), "/", "-"), Password: "test"}
	if err := db.Create(&inactiveAuthor).Error; err != nil {
		t.Fatal(err)
	}
	inactiveAuthorPost := models.Post{
		Model:    gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID: inactiveAuthor.ID, Content: "inactive author reference content", Visibility: "public",
	}
	if err := db.Create(&inactiveAuthorPost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&inactiveAuthor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}
	ids := []uint{deletedPost.ID, unavailablePost.ID, futurePost.ID, inactiveAuthorPost.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", inactiveAuthor.ID).Delete(&models.User{})
	})

	activeID := fixture.Article.ID
	activeReference, err := loadPostReferenceFromDB(db, &activeID, now)
	if err != nil {
		t.Fatal(err)
	}
	if activeReference == nil || activeReference.Deleted || activeReference.Author == nil || activeReference.Content == "" {
		t.Fatalf("active post reference=%#v", activeReference)
	}
	assertActivePostReferenceWire(t, activeReference)

	normalActive := models.Post{
		Model:    gorm.Model{CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
		AuthorID: fixture.Author.ID, Content: "normal active reference", Visibility: "public",
	}
	if err := db.Create(&normalActive).Error; err != nil {
		t.Fatal(err)
	}
	ids = append(ids, normalActive.ID)
	normalActiveReference, err := loadPostReferenceFromDB(db, &normalActive.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if normalActiveReference == nil || normalActiveReference.Deleted {
		t.Fatalf("normal active reference=%#v", normalActiveReference)
	}
	assertActivePostReferenceWire(t, normalActiveReference)

	deleted, err := loadPostReferenceFromDB(db, &deletedPost.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	assertPostReferenceTombstone(t, deleted)

	unavailable, err := loadPostReferenceFromDB(db, &unavailablePost.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable == nil || unavailable.Deleted || unavailable.Content != unavailablePost.Content {
		t.Fatalf("active post reference=%#v", unavailable)
	}
	assertActivePostReferenceWire(t, unavailable)

	future, err := loadPostReferenceFromDB(db, &futurePost.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if future == nil || future.Deleted || future.Content != futurePost.Content {
		t.Fatalf("future normal reference=%#v", future)
	}
	assertActivePostReferenceWire(t, future)

	inactive, err := loadPostReferenceFromDB(db, &inactiveAuthorPost.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	assertPostReferenceTombstone(t, inactive)

	missingID := unavailablePost.ID + 1000000
	missing, err := loadPostReferenceFromDB(db, &missingID, now)
	if err != nil {
		t.Fatal(err)
	}
	assertPostReferenceTombstone(t, missing)
}

func assertActivePostReferenceWire(t *testing.T, reference *postReferenceResponse) {
	t.Helper()
	payload, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]bool{"id": true, "author": true, "content": true, "published_at": true, "media": true, "deleted": true}
	if len(decoded) != len(wantKeys) {
		t.Fatalf("active reference keys=%v payload=%s", decoded, payload)
	}
	for key := range wantKeys {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("active reference missing %q: %s", key, payload)
		}
	}
	var deleted bool
	if err := json.Unmarshal(decoded["deleted"], &deleted); err != nil || deleted {
		t.Fatalf("active deleted=%t err=%v payload=%s", deleted, err, payload)
	}
	if string(decoded["media"]) != "[]" {
		t.Fatalf("active reference media=%s want []", decoded["media"])
	}
}

func assertPostReferenceTombstone(t *testing.T, reference *postReferenceResponse) {
	t.Helper()
	if reference == nil || reference.ID == 0 || !reference.Deleted {
		t.Fatalf("reference=%#v want deleted tombstone", reference)
	}
	payload, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 {
		t.Fatalf("tombstone keys=%v payload=%s", decoded, payload)
	}
	if _, ok := decoded["id"]; !ok {
		t.Fatalf("tombstone missing id: %s", payload)
	}
	var deleted bool
	if err := json.Unmarshal(decoded["deleted"], &deleted); err != nil || !deleted {
		t.Fatalf("tombstone deleted=%t err=%v payload=%s", deleted, err, payload)
	}
	for _, forbidden := range []string{"state", "author", "content", "published_at", "media"} {
		if _, ok := decoded[forbidden]; ok {
			t.Fatalf("tombstone leaked %s: %s", forbidden, payload)
		}
	}
}

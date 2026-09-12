package controllers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

func TestCreatePostRejectsInvalidIdempotencyKey(t *testing.T) {
	originalPersist := persistPostGraphFn
	called := false
	persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
		called = true
		return nil
	}
	t.Cleanup(func() { persistPostGraphFn = originalPersist })

	recorder := executeCreatePostRequestWithKey(t, `{"content":"valid post"}`, "not-a-uuid")
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"POST_INVALID_IDEMPOTENCY_KEY"`) ||
		!strings.Contains(recorder.Body.String(), `"error":"invalid Idempotency-Key"`) {
		t.Fatalf("unexpected invalid-key response: %s", recorder.Body.String())
	}
}

func TestCreatePostPersistsCanonicalIdempotencyFieldsOnFirstCreate(t *testing.T) {
	key := uuid.New()
	originalLookup := loadClientPublishPostFn
	originalAuthor := loadPostAuthorForCreate
	originalPersist := persistPostGraphFn
	lookupCalls := 0
	var persisted models.Post
	var persistedRequest createPostRequest
	loadClientPublishPostFn = func(uint, uuid.UUID) (models.Post, error) {
		lookupCalls++
		return models.Post{}, gorm.ErrRecordNotFound
	}
	loadPostAuthorForCreate = func(id uint) (publicAuthorResponse, error) {
		return publicAuthorResponse{ID: id, Username: "alice"}, nil
	}
	persistPostGraphFn = func(post *models.Post, userID uint, content string, req createPostRequest, _ []validatedPostMedia, now time.Time) error {
		persistedRequest = req
		*post = models.Post{
			Model:      gorm.Model{ID: 71, CreatedAt: now, UpdatedAt: now},
			AuthorID:   userID,
			Content:    content,
			Language:   "und",
			Visibility: "public",
		}
		persisted = *post
		return nil
	}
	t.Cleanup(func() {
		loadClientPublishPostFn = originalLookup
		loadPostAuthorForCreate = originalAuthor
		persistPostGraphFn = originalPersist
	})

	recorder := executeCreatePostRequestWithKey(t, `{"content":"  first post  "}`, key.String())
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if lookupCalls != 1 {
		t.Fatalf("lookup calls=%d want 1", lookupCalls)
	}
	if persistedRequest.ClientPublishID == nil || *persistedRequest.ClientPublishID != key {
		t.Fatalf("persisted id=%v want %s", persistedRequest.ClientPublishID, key)
	}
	if persistedRequest.ClientPublishFingerprint == nil || len(*persistedRequest.ClientPublishFingerprint) != 64 {
		t.Fatalf("persisted fingerprint=%v", persistedRequest.ClientPublishFingerprint)
	}
	if persisted.ClientPublishID != nil || persisted.ClientPublishFingerprint != nil {
		t.Fatalf("test persistence unexpectedly changed post fields: %#v", persisted)
	}
	if recorder.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("first create incorrectly marked as replay")
	}
}

func TestCreatePostReplaysSamePayloadWithoutCreateSideEffects(t *testing.T) {
	key := uuid.New()
	fingerprint, err := createPostPayloadFingerprint("same post", createPostRequest{Content: "same post"})
	if err != nil {
		t.Fatal(err)
	}
	fingerprintCopy := fingerprint
	originalLookup := loadClientPublishPostFn
	originalResponse := buildStoredPostCreateResponseFn
	originalPersist := persistPostGraphFn
	originalInitialize := initializePostLikeState
	originalInvalidate := invalidatePostCreateParentDetailCache
	lookupCalls := 0
	persistCalls := 0
	initializeCalls := 0
	invalidateCalls := 0
	loadClientPublishPostFn = func(authorID uint, clientPublishID uuid.UUID) (models.Post, error) {
		lookupCalls++
		return models.Post{
			Model:                    gorm.Model{ID: 72},
			AuthorID:                 authorID,
			ClientPublishID:          &clientPublishID,
			ClientPublishFingerprint: &fingerprintCopy,
		}, nil
	}
	buildStoredPostCreateResponseFn = func(post models.Post, _ time.Time) (postResponse, error) {
		return postResponse{ID: post.ID, Content: post.Content, Media: []postMediaResponse{}}, nil
	}
	persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
		persistCalls++
		return nil
	}
	initializePostLikeState = func(uint) error {
		initializeCalls++
		return nil
	}
	invalidatePostCreateParentDetailCache = func(uint) error {
		invalidateCalls++
		return nil
	}
	t.Cleanup(func() {
		loadClientPublishPostFn = originalLookup
		buildStoredPostCreateResponseFn = originalResponse
		persistPostGraphFn = originalPersist
		initializePostLikeState = originalInitialize
		invalidatePostCreateParentDetailCache = originalInvalidate
	})

	recorder := executeCreatePostRequestWithKey(t, `{"content":"same post"}`, key.String())
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("status=%d replay=%q body=%s", recorder.Code, recorder.Header().Get("Idempotency-Replayed"), recorder.Body.String())
	}
	if lookupCalls != 1 || persistCalls != 0 || initializeCalls != 0 || invalidateCalls != 0 {
		t.Fatalf("lookup=%d persist=%d initialize=%d invalidate=%d", lookupCalls, persistCalls, initializeCalls, invalidateCalls)
	}
}

func TestCreatePostRejectsIdempotencyPayloadConflictAndDeletedReplay(t *testing.T) {
	key := uuid.New()
	originalLookup := loadClientPublishPostFn
	originalResponse := buildStoredPostCreateResponseFn
	lookupPost := models.Post{}
	loadClientPublishPostFn = func(uint, uuid.UUID) (models.Post, error) { return lookupPost, nil }
	buildStoredPostCreateResponseFn = func(models.Post, time.Time) (postResponse, error) {
		t.Fatal("stored response should not be built for conflict")
		return postResponse{}, nil
	}
	t.Cleanup(func() {
		loadClientPublishPostFn = originalLookup
		buildStoredPostCreateResponseFn = originalResponse
	})

	fingerprint := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lookupPost.ClientPublishFingerprint = &fingerprint
	conflict := executeCreatePostRequestWithKey(t, `{"content":"different post"}`, key.String())
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"POST_IDEMPOTENCY_CONFLICT"`) {
		t.Fatalf("status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	lookupPost.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
	deleted := executeCreatePostRequestWithKey(t, `{"content":"different post"}`, key.String())
	if deleted.Code != http.StatusConflict {
		t.Fatalf("deleted status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestCreatePostRetriesUniqueCollisionAsReplay(t *testing.T) {
	key := uuid.New()
	fingerprint, err := createPostPayloadFingerprint("concurrent", createPostRequest{Content: "concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	fingerprintCopy := fingerprint
	originalLookup := loadClientPublishPostFn
	originalResponse := buildStoredPostCreateResponseFn
	originalPersist := persistPostGraphFn
	originalAuthor := loadPostAuthorForCreate
	lookupCalls := 0
	persistCalls := 0
	loadClientPublishPostFn = func(authorID uint, clientPublishID uuid.UUID) (models.Post, error) {
		lookupCalls++
		if lookupCalls == 1 {
			return models.Post{}, gorm.ErrRecordNotFound
		}
		return models.Post{
			Model:                    gorm.Model{ID: 73},
			AuthorID:                 authorID,
			ClientPublishID:          &clientPublishID,
			ClientPublishFingerprint: &fingerprintCopy,
		}, nil
	}
	loadPostAuthorForCreate = func(id uint) (publicAuthorResponse, error) {
		return publicAuthorResponse{ID: id, Username: "alice"}, nil
	}
	persistPostGraphFn = func(*models.Post, uint, string, createPostRequest, []validatedPostMedia, time.Time) error {
		persistCalls++
		return &pgconn.PgError{Code: "23505", ConstraintName: "uidx_posts_author_client_publish_id"}
	}
	buildStoredPostCreateResponseFn = func(post models.Post, _ time.Time) (postResponse, error) {
		return postResponse{ID: post.ID, Content: "concurrent", Media: []postMediaResponse{}}, nil
	}
	t.Cleanup(func() {
		loadClientPublishPostFn = originalLookup
		buildStoredPostCreateResponseFn = originalResponse
		persistPostGraphFn = originalPersist
		loadPostAuthorForCreate = originalAuthor
	})

	recorder := executeCreatePostRequestWithKey(t, `{"content":"concurrent"}`, key.String())
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("status=%d replay=%q body=%s", recorder.Code, recorder.Header().Get("Idempotency-Replayed"), recorder.Body.String())
	}
	if lookupCalls != 2 {
		t.Fatalf("lookup calls=%d want 2", lookupCalls)
	}
}

func TestCreatePostPayloadFingerprintNormalizesContentAndMediaOrder(t *testing.T) {
	first := createPostRequest{
		ReplyToPostID: uintPointer(9),
		Media:         []createPostMediaRequest{{Type: "image", URL: " /media/a "}, {Type: "image", URL: "/media/b"}},
	}
	second := createPostRequest{
		ReplyToPostID: uintPointer(9),
		Media:         []createPostMediaRequest{{Type: " image ", URL: "/media/a"}, {Type: "image", URL: "/media/b"}},
	}
	firstFingerprint, err := createPostPayloadFingerprint("  content ", first)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := createPostPayloadFingerprint("content", second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint != secondFingerprint || len(firstFingerprint) != 64 {
		t.Fatalf("normalized fingerprints differ: %q vs %q", firstFingerprint, secondFingerprint)
	}
	reversed := second
	reversed.Media = []createPostMediaRequest{{Type: "image", URL: "/media/b"}, {Type: "image", URL: "/media/a"}}
	reversedFingerprint, err := createPostPayloadFingerprint("content", reversed)
	if err != nil {
		t.Fatal(err)
	}
	if reversedFingerprint == firstFingerprint {
		t.Fatal("media order was omitted from fingerprint")
	}
}

func TestIsClientPublishUniqueViolationOnlyAcceptsPublishIdentityConstraint(t *testing.T) {
	if !isClientPublishUniqueViolation(&pgconn.PgError{
		Code: "23505", ConstraintName: clientPublishUniqueIndex,
	}) {
		t.Fatal("publish identity unique violation was not recognized")
	}
	if isClientPublishUniqueViolation(&pgconn.PgError{
		Code: "23505", ConstraintName: "some_other_unique_index",
	}) {
		t.Fatal("unrelated unique violation was classified as an idempotency collision")
	}
	if isClientPublishUniqueViolation(&pgconn.PgError{Code: "23514"}) {
		t.Fatal("check violation was classified as an idempotency collision")
	}
	if !isClientPublishUniqueViolation(gorm.ErrDuplicatedKey) {
		t.Fatal("GORM duplicate error was not recognized for exact-key reconciliation")
	}
}

func executeCreatePostRequestWithKey(t *testing.T, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("user_id", uint(7))
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/posts", bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Request.Header.Set("Idempotency-Key", key)
	NewCreatePostHandler()(ctx)
	return recorder
}

func uintPointer(value uint) *uint { return &value }

package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
)

func TestParsePostMediaObjectURL(t *testing.T) {
	const validUUID = "550e8400-e29b-41d4-a716-446655440000"
	cases := []struct {
		name       string
		url        string
		wantObject string
		wantValid  bool
	}{
		{name: "jpeg", url: "/api/files/post-media/123/" + validUUID + ".jpg", wantObject: "post-media/123/" + validUUID + ".jpg", wantValid: true},
		{name: "png", url: "/api/files/post-media/123/" + validUUID + ".png", wantObject: "post-media/123/" + validUUID + ".png", wantValid: true},
		{name: "webp", url: "/api/files/post-media/123/" + validUUID + ".webp", wantObject: "post-media/123/" + validUUID + ".webp", wantValid: true},
		{name: "wrong owner", url: "/api/files/post-media/124/" + validUUID + ".jpg"},
		{name: "prefix owner collision", url: "/api/files/post-media/1234/" + validUUID + ".jpg"},
		{name: "malformed uuid", url: "/api/files/post-media/123/not-a-uuid.jpg"},
		{name: "unsupported extension", url: "/api/files/post-media/123/" + validUUID + ".gif"},
		{name: "missing filename", url: "/api/files/post-media/123/"},
		{name: "profile avatar", url: "/api/files/profile-avatars/123/" + validUUID + ".jpg"},
		{name: "article cover", url: "/api/files/article-covers/" + validUUID + ".jpg"},
		{name: "external url", url: "https://example.com/a.jpg"},
		{name: "protocol relative url", url: "//example.com/a.jpg"},
		{name: "path traversal", url: "/api/files/post-media/123/../a.jpg"},
		{name: "query string", url: "/api/files/post-media/123/" + validUUID + ".jpg?download=1"},
		{name: "fragment", url: "/api/files/post-media/123/" + validUUID + ".jpg#part"},
		{name: "carriage return", url: "/api/files/post-media/123/" + validUUID + ".jpg\r\nX-Test: bad"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			objectKey, err := parsePostMediaObjectURL(123, testCase.url)
			if testCase.wantValid {
				if err != nil || objectKey != testCase.wantObject {
					t.Fatalf("object=%q err=%v want object=%q", objectKey, err, testCase.wantObject)
				}
				return
			}
			if !errors.Is(err, errInvalidPostMedia) {
				t.Fatalf("err=%v want invalid media", err)
			}
		})
	}
}

func TestValidatePostMediaRequestsClassifiesStorageErrors(t *testing.T) {
	originalStat := statStoredObject
	t.Cleanup(func() { statStoredObject = originalStat })
	request := []createPostMediaRequest{{Type: "image", URL: "/api/files/post-media/123/550e8400-e29b-41d4-a716-446655440000.jpg"}}

	statStoredObject = func(context.Context, string) error { return nil }
	validated, err := validatePostMediaRequests(context.Background(), 123, request)
	if err != nil || len(validated) != 1 || validated[0].ObjectKey == "" {
		t.Fatalf("existing object validation=%#v err=%v", validated, err)
	}

	statStoredObject = func(context.Context, string) error { return errPostMediaObjectUnavailable }
	_, err = validatePostMediaRequests(context.Background(), 123, request)
	if !errors.Is(err, errPostMediaObjectUnavailable) {
		t.Fatalf("missing object err=%v", err)
	}

	statStoredObject = func(context.Context, string) error { return errors.New("minio unavailable") }
	_, err = validatePostMediaRequests(context.Background(), 123, request)
	if !errors.Is(err, errPostMediaStorageUnavailable) {
		t.Fatalf("infrastructure error=%v", err)
	}
}

func TestValidatePostMediaRequestsRejectsCountTypeAndDuplicates(t *testing.T) {
	originalStat := statStoredObject
	t.Cleanup(func() { statStoredObject = originalStat })
	statStoredObject = func(context.Context, string) error { return nil }
	validURL := "/api/files/post-media/123/550e8400-e29b-41d4-a716-446655440000.jpg"
	tooMany := make([]createPostMediaRequest, maxPostMediaCount+1)
	for index := range tooMany {
		tooMany[index] = createPostMediaRequest{Type: "image", URL: validURL}
	}
	cases := []struct {
		name  string
		items []createPostMediaRequest
	}{
		{name: "too many", items: tooMany},
		{name: "unsupported type", items: []createPostMediaRequest{{Type: "video", URL: validURL}}},
		{name: "duplicate url", items: []createPostMediaRequest{{Type: "image", URL: validURL}, {Type: "image", URL: validURL}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := validatePostMediaRequests(context.Background(), 123, testCase.items)
			if !errors.Is(err, errInvalidPostMedia) {
				t.Fatalf("err=%v want invalid media", err)
			}
		})
	}
}

func TestLoadPostMediaByPostIDsFromDBBatchesAndOrdersRowsIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	second := models.Post{AuthorID: fixture.Author.ID, Content: "second media fixture", Visibility: "public"}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", []uint{fixture.Article.ID, second.ID}).Delete(&models.PostMedia{})
		db.Unscoped().Where("id = ?", second.ID).Delete(&models.Post{})
	})
	rows := []models.PostMedia{
		{PostID: fixture.Article.ID, MediaType: "image", URL: "/api/files/post-media/1/a.jpg", Position: 1},
		{PostID: fixture.Article.ID, MediaType: "image", URL: "/api/files/post-media/1/b.jpg", Position: 0},
		{PostID: second.ID, MediaType: "image", URL: "/api/files/post-media/1/c.jpg", Position: 0},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	mediaByPostID, err := loadPostMediaByPostIDsFromDB(db, []uint{second.ID, 0, fixture.Article.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(mediaByPostID[fixture.Article.ID]) != 2 || mediaByPostID[fixture.Article.ID][0].Position != 0 || mediaByPostID[fixture.Article.ID][1].Position != 1 {
		t.Fatalf("article media=%#v", mediaByPostID[fixture.Article.ID])
	}
	if len(mediaByPostID[second.ID]) != 1 || mediaByPostID[second.ID][0].URL != "/api/files/post-media/1/c.jpg" {
		t.Fatalf("second media=%#v", mediaByPostID[second.ID])
	}
}

func TestLoadPostMediaByPostIDsFromDBSkipsQueryForEmptyIDs(t *testing.T) {
	mediaByPostID, err := loadPostMediaByPostIDsFromDB(nil, []uint{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(mediaByPostID) != 0 {
		t.Fatalf("media=%#v want empty map", mediaByPostID)
	}
}

func TestPersistPostGraphRollsBackPostWhenMediaInsertFailsIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	const constraintName = "chk_post_media_test_forced_failure"
	db.Exec("ALTER TABLE post_media DROP CONSTRAINT IF EXISTS " + constraintName)
	if err := db.Exec("ALTER TABLE post_media ADD CONSTRAINT " + constraintName + " CHECK (position < 0)").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec("ALTER TABLE post_media DROP CONSTRAINT IF EXISTS " + constraintName) })

	previousDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previousDB })
	var post models.Post
	media := []validatedPostMedia{{MediaType: "image", PublicURL: "/api/files/post-media/1/550e8400-e29b-41d4-a716-446655440000.jpg", ObjectKey: "post-media/1/550e8400-e29b-41d4-a716-446655440000.jpg"}}
	err := persistPostGraph(&post, fixture.Author.ID, "atomic media failure", createPostRequest{Content: "atomic media failure"}, media, time.Now().UTC())
	if err == nil {
		t.Fatal("persist unexpectedly succeeded")
	}
	var postCount, mediaCount int64
	if db.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Count(&postCount).Error != nil {
		t.Fatal("failed to count rolled-back Post")
	}
	if db.Model(&models.PostMedia{}).Where("post_id = ?", post.ID).Count(&mediaCount).Error != nil {
		t.Fatal("failed to count rolled-back PostMedia")
	}
	if postCount != 0 || mediaCount != 0 {
		t.Fatalf("transaction left partial graph: posts=%d media=%d", postCount, mediaCount)
	}
}

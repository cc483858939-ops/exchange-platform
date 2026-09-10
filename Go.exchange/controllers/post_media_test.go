package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/postmedia"
)

func TestParsePostMediaObjectURL(t *testing.T) {
	const validUUID = "550e8400-e29b-41d4-a716-446655440000"
	cases := []struct {
		name      string
		url       string
		wantValid bool
	}{
		{name: "jpeg", url: "/api/files/post-media/users/v1/123/" + validUUID + "/medium.jpg", wantValid: true},
		{name: "png", url: "/api/files/post-media/users/v1/123/" + validUUID + "/medium.png", wantValid: true},
		{name: "wrong owner", url: "/api/files/post-media/users/v1/124/" + validUUID + "/medium.jpg"},
		{name: "prefix owner collision", url: "/api/files/post-media/users/v1/1234/" + validUUID + "/medium.jpg"},
		{name: "malformed uuid", url: "/api/files/post-media/users/v1/123/not-a-uuid/medium.jpg"},
		{name: "uppercase uuid", url: "/api/files/post-media/users/v1/123/" + strings.ToUpper(validUUID) + "/medium.jpg"},
		{name: "large variant", url: "/api/files/post-media/users/v1/123/" + validUUID + "/large.jpg"},
		{name: "original variant", url: "/api/files/post-media/users/v1/123/" + validUUID + "/original.jpg"},
		{name: "manifest", url: "/api/files/post-media/users/v1/123/" + validUUID + "/manifest.json"},
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
			parsed, err := parsePostMediaObjectURL(123, testCase.url)
			if testCase.wantValid {
				wantObject := strings.TrimPrefix(testCase.url, "/api/files/")
				if err != nil || parsed.MediumObjectKey != wantObject || parsed.MediaID != validUUID || parsed.ManifestObjectKey != "post-media/users/v1/123/"+validUUID+"/manifest.json" {
					t.Fatalf("parsed=%#v err=%v want medium=%q", parsed, err, wantObject)
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
	originalRead := readStoredObject
	t.Cleanup(func() {
		statStoredObject = originalStat
		readStoredObject = originalRead
	})
	request := []createPostMediaRequest{{Type: "image", URL: "/api/files/post-media/users/v1/123/550e8400-e29b-41d4-a716-446655440000/medium.jpg"}}

	readStoredObject = func(context.Context, string, int64) ([]byte, error) {
		return testPostMediaManifestJSON(), nil
	}
	statStoredObject = func(context.Context, string) error { return nil }
	validated, err := validatePostMediaRequests(context.Background(), 123, request)
	if err != nil || len(validated) != 1 || validated[0].MediumObjectKey == "" || validated[0].LargeURL == "" || validated[0].Width != 1200 || validated[0].Height != 800 {
		t.Fatalf("existing object validation=%#v err=%v", validated, err)
	}

	readStoredObject = func(context.Context, string, int64) ([]byte, error) { return nil, errPostMediaObjectUnavailable }
	_, err = validatePostMediaRequests(context.Background(), 123, request)
	if !errors.Is(err, errPostMediaObjectUnavailable) {
		t.Fatalf("missing object err=%v", err)
	}

	readStoredObject = func(context.Context, string, int64) ([]byte, error) { return nil, errors.New("minio unavailable") }
	_, err = validatePostMediaRequests(context.Background(), 123, request)
	if !errors.Is(err, errPostMediaStorageUnavailable) {
		t.Fatalf("infrastructure error=%v", err)
	}
}

func TestValidatePostMediaRequestsRejectsCountTypeAndDuplicates(t *testing.T) {
	originalStat := statStoredObject
	originalRead := readStoredObject
	t.Cleanup(func() {
		statStoredObject = originalStat
		readStoredObject = originalRead
	})
	statStoredObject = func(context.Context, string) error { return nil }
	readStoredObject = func(context.Context, string, int64) ([]byte, error) { return testPostMediaManifestJSON(), nil }
	validURL := "/api/files/post-media/users/v1/123/550e8400-e29b-41d4-a716-446655440000/medium.jpg"
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

func TestValidatePostMediaRequestsRejectsInvalidManifestAndVariantState(t *testing.T) {
	originalStat := statStoredObject
	originalRead := readStoredObject
	t.Cleanup(func() {
		statStoredObject = originalStat
		readStoredObject = originalRead
	})
	const mediaID = "550e8400-e29b-41d4-a716-446655440000"
	validURL := "/api/files/post-media/users/v1/123/" + mediaID + "/medium.jpg"
	baseManifest := postmedia.Manifest{}
	if err := json.Unmarshal(testPostMediaManifestJSON(), &baseManifest); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name          string
		mutate        func(*postmedia.Manifest)
		body          []byte
		missingMedium bool
		missingLarge  bool
		wantErr       error
	}{
		{name: "malformed json", body: []byte("{"), wantErr: errInvalidPostMedia},
		{name: "wrong version", mutate: func(manifest *postmedia.Manifest) { manifest.Version = 2 }, wantErr: errInvalidPostMedia},
		{name: "wrong owner", mutate: func(manifest *postmedia.Manifest) { manifest.OwnerID = 124 }, wantErr: errInvalidPostMedia},
		{name: "wrong media id", mutate: func(manifest *postmedia.Manifest) { manifest.MediaID = "550e8400-e29b-41d4-a716-446655440001" }, wantErr: errInvalidPostMedia},
		{name: "wrong medium key", mutate: func(manifest *postmedia.Manifest) {
			manifest.Medium.ObjectKey = "post-media/users/v1/123/" + mediaID + "/medium.png"
		}, wantErr: errInvalidPostMedia},
		{name: "wrong large key", mutate: func(manifest *postmedia.Manifest) {
			manifest.Large.ObjectKey = "post-media/users/v1/123/" + mediaID + "/large.png"
		}, wantErr: errInvalidPostMedia},
		{name: "invalid medium dimensions", mutate: func(manifest *postmedia.Manifest) { manifest.Medium.Width = 1201 }, wantErr: errInvalidPostMedia},
		{name: "invalid large dimensions", mutate: func(manifest *postmedia.Manifest) { manifest.Large.Width = 2049 }, wantErr: errInvalidPostMedia},
		{name: "missing medium object", missingMedium: true, wantErr: errPostMediaObjectUnavailable},
		{name: "missing large object", missingLarge: true, wantErr: errPostMediaObjectUnavailable},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := baseManifest
			if testCase.mutate != nil {
				testCase.mutate(&manifest)
			}
			readStoredObject = func(context.Context, string, int64) ([]byte, error) {
				if testCase.body != nil {
					return testCase.body, nil
				}
				body, err := json.Marshal(manifest)
				return body, err
			}
			statStoredObject = func(_ context.Context, objectKey string) error {
				if testCase.missingMedium && objectKey == baseManifest.Medium.ObjectKey {
					return errPostMediaObjectUnavailable
				}
				if testCase.missingLarge && objectKey == baseManifest.Large.ObjectKey {
					return errPostMediaObjectUnavailable
				}
				return nil
			}

			_, err := validatePostMediaRequests(context.Background(), 123, []createPostMediaRequest{{Type: "image", URL: validURL}})
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("err=%v want=%v", err, testCase.wantErr)
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
		{PostID: fixture.Article.ID, MediaType: "image", URL: "/medium-a.jpg", LargeURL: "/large-a.jpg", Width: 1200, Height: 800, Position: 1},
		{PostID: fixture.Article.ID, MediaType: "image", URL: "/medium-b.jpg", LargeURL: "/large-b.jpg", Width: 800, Height: 600, Position: 0},
		{PostID: second.ID, MediaType: "image", URL: "/medium-c.jpg", LargeURL: "/large-c.jpg", Width: 1200, Height: 800, Position: 0},
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
	if len(mediaByPostID[second.ID]) != 1 || mediaByPostID[second.ID][0].URL != "/medium-c.jpg" || mediaByPostID[second.ID][0].LargeURL != "/large-c.jpg" || mediaByPostID[second.ID][0].Width != 1200 || mediaByPostID[second.ID][0].Height != 800 {
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
	media := []validatedPostMedia{{MediaType: "image", PublicURL: "/api/files/post-media/users/v1/1/550e8400-e29b-41d4-a716-446655440000/medium.jpg", LargeURL: "/api/files/post-media/users/v1/1/550e8400-e29b-41d4-a716-446655440000/large.jpg", Width: 1200, Height: 800, MediumObjectKey: "post-media/users/v1/1/550e8400-e29b-41d4-a716-446655440000/medium.jpg"}}
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

func testPostMediaManifestJSON() []byte {
	return testPostMediaManifestJSONFor(123, "550e8400-e29b-41d4-a716-446655440000")
}

func testPostMediaManifestJSONFor(ownerID uint, mediaID string) []byte {
	prefix := "post-media/users/v1/" + strconv.FormatUint(uint64(ownerID), 10) + "/" + mediaID + "/"
	body, _ := json.Marshal(postmedia.Manifest{
		Version:           1,
		OwnerID:           ownerID,
		MediaID:           mediaID,
		OriginalObjectKey: prefix + "original.jpg",
		Medium: postmedia.VariantManifest{
			ObjectKey:   prefix + "medium.jpg",
			ContentType: "image/jpeg", Width: 1200, Height: 800,
		},
		Large: postmedia.VariantManifest{
			ObjectKey:   prefix + "large.jpg",
			ContentType: "image/jpeg", Width: 2048, Height: 1365,
		},
	})
	return body
}

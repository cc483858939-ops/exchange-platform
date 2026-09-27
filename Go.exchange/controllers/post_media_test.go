package controllers

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/postmedia"
	"Go.exchange/postmediaupload"
	"github.com/google/uuid"
	"gorm.io/gorm"
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
				if err != nil || parsed.MediaID != validUUID {
					t.Fatalf("parsed=%#v err=%v want media id=%q", parsed, err, validUUID)
				}
				return
			}
			if !errors.Is(err, errInvalidPostMedia) {
				t.Fatalf("err=%v want invalid media", err)
			}
		})
	}
}

func TestValidatePostMediaRequestsParsesIdentityWithoutStorageValidation(t *testing.T) {
	const mediaID = "550e8400-e29b-41d4-a716-446655440000"
	validURL := "/api/files/post-media/users/v1/123/" + mediaID + "/medium.jpg"
	validated, err := validatePostMediaRequests(context.Background(), 123, []createPostMediaRequest{{Type: "image", URL: validURL}})
	if err != nil || len(validated) != 1 || validated[0].MediaID != mediaID || validated[0].PublicURL != validURL || validated[0].MediaType != "image" {
		t.Fatalf("parsed media=%#v err=%v", validated, err)
	}
	if validated[0].LargeURL != "" || validated[0].Width != 0 || validated[0].Height != 0 {
		t.Fatalf("storage metadata was loaded outside transactional consumption: %#v", validated[0])
	}
}

func TestValidatePostMediaRequestsRejectsCountTypeAndDuplicateIdentity(t *testing.T) {
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
		{name: "same media id with different variant suffix", items: []createPostMediaRequest{
			{Type: "image", URL: validURL},
			{Type: "image", URL: strings.TrimSuffix(validURL, ".jpg") + ".png"},
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := validatePostMediaRequests(context.Background(), 123, testCase.items)
			if err != errInvalidPostMedia {
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
	mediaID := "550e8400-e29b-41d4-a716-446655440000"
	pendingUpload, mediaURL := newTestPostMediaUpload(t, fixture.Author.ID, mediaID, postmediaupload.StatusUploaded)
	if err := db.Create(&pendingUpload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}) })
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
	media := []validatedPostMedia{{MediaID: mediaID, MediaType: "image", PublicURL: mediaURL}}
	err := persistPostGraph(context.Background(), &post, fixture.Author.ID, "atomic media failure", createPostRequest{Content: "atomic media failure"}, media, time.Now().UTC())
	if err == nil {
		t.Fatal("persist unexpectedly succeeded")
	}
	var postCount, mediaCount, pendingCount int64
	if db.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Count(&postCount).Error != nil {
		t.Fatal("failed to count rolled-back Post")
	}
	if db.Model(&models.PostMedia{}).Where("post_id = ?", post.ID).Count(&mediaCount).Error != nil {
		t.Fatal("failed to count rolled-back PostMedia")
	}
	if db.Model(&models.PostMediaUpload{}).Where("media_id = ?", mediaID).Count(&pendingCount).Error != nil {
		t.Fatal("failed to count rolled-back pending upload")
	}
	if postCount != 0 || mediaCount != 0 || pendingCount != 1 {
		t.Fatalf("transaction left partial graph or consumed upload: posts=%d media=%d pending=%d", postCount, mediaCount, pendingCount)
	}
}

func TestPersistPostGraphConsumesUploadedMediaInTransactionIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	mediaID := "550e8400-e29b-41d4-a716-446655440001"
	pendingUpload, mediaURL := newTestPostMediaUpload(t, fixture.Author.ID, mediaID, postmediaupload.StatusUploaded)
	if err := db.Create(&pendingUpload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}) })
	var post models.Post
	media := []validatedPostMedia{{MediaID: mediaID, MediaType: "image", PublicURL: mediaURL}}
	if err := persistPostGraph(context.Background(), &post, fixture.Author.ID, "media transaction", createPostRequest{Content: "media transaction"}, media, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", post.ID).Delete(&models.PostMedia{})
		db.Unscoped().Delete(&models.Post{}, post.ID)
	})
	var bound []models.PostMedia
	if err := db.Where("post_id = ?", post.ID).Find(&bound).Error; err != nil {
		t.Fatal(err)
	}
	var pendingCount int64
	if err := db.Model(&models.PostMediaUpload{}).Where("media_id = ?", mediaID).Count(&pendingCount).Error; err != nil {
		t.Fatal(err)
	}
	if len(bound) != 1 || pendingCount != 0 || bound[0].URL != mediaURL || bound[0].LargeURL != pendingUpload.LargeURL ||
		bound[0].Width != pendingUpload.Width || bound[0].Height != pendingUpload.Height || media[0].LargeURL != pendingUpload.LargeURL {
		t.Fatalf("bound=%#v pending=%d validated=%#v", bound, pendingCount, media)
	}
}

func TestExpiredUploadedMediaRemainsBindableUntilGCClaimsItIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	mediaID := uuid.NewString()
	pendingUpload, mediaURL := newTestPostMediaUpload(t, fixture.Author.ID, mediaID, postmediaupload.StatusUploaded)
	now := time.Now().UTC().Truncate(time.Microsecond)
	uploadedAt := now.Add(-25 * time.Hour)
	pendingUpload.UploadedAt = &uploadedAt
	pendingUpload.CleanupAfter = now.Add(-time.Hour)
	if err := db.Create(&pendingUpload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}) })

	previousDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previousDB })
	var post models.Post
	media := []validatedPostMedia{{MediaID: mediaID, MediaType: "image", PublicURL: mediaURL}}
	if err := persistPostGraph(context.Background(), &post, fixture.Author.ID, "grace expired but unclaimed", createPostRequest{Content: "grace expired but unclaimed"}, media, now); err != nil {
		t.Fatalf("expired but unclaimed upload was rejected: %v", err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", post.ID).Delete(&models.PostMedia{})
		db.Unscoped().Delete(&models.Post{}, post.ID)
	})
	var boundCount, pendingCount int64
	if err := db.Model(&models.PostMedia{}).Where("post_id = ?", post.ID).Count(&boundCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.PostMediaUpload{}).Where("media_id = ?", mediaID).Count(&pendingCount).Error; err != nil {
		t.Fatal(err)
	}
	if boundCount != 1 || pendingCount != 0 {
		t.Fatalf("expired unclaimed upload state bound=%d pending=%d", boundCount, pendingCount)
	}
}

func TestPostMediaGCClaimWinsAgainstCreateIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	mediaID := uuid.NewString()
	pendingUpload, mediaURL := newTestPostMediaUpload(t, fixture.Author.ID, mediaID, postmediaupload.StatusUploaded)
	now := time.Now().UTC().Truncate(time.Microsecond)
	uploadedAt := now.Add(-25 * time.Hour)
	pendingUpload.UploadedAt = &uploadedAt
	pendingUpload.CleanupAfter = now.Add(-time.Hour)
	if err := db.Create(&pendingUpload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}) })
	claims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, now, 15*time.Minute, 10)
	if err != nil || len(claims) != 1 || claims[0].MediaID != mediaID {
		t.Fatalf("GC claim=%+v err=%v", claims, err)
	}

	previousDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previousDB })
	var post models.Post
	media := []validatedPostMedia{{MediaID: mediaID, MediaType: "image", PublicURL: mediaURL}}
	err = persistPostGraph(context.Background(), &post, fixture.Author.ID, "gc already claimed media", createPostRequest{Content: "gc already claimed media"}, media, now)
	if err != errInvalidPostMedia {
		t.Fatalf("create error=%v want invalid media after GC claim", err)
	}
	var postCount, mediaCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.PostMedia{}).Where("post_id = ?", post.ID).Count(&mediaCount).Error; err != nil {
		t.Fatal(err)
	}
	var stored models.PostMediaUpload
	if err := db.Where("media_id = ?", mediaID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 0 || mediaCount != 0 || stored.Status != postmediaupload.StatusCleanupPending || stored.CleanupClaimToken == nil || *stored.CleanupClaimToken != claims[0].ClaimToken {
		t.Fatalf("failed create did not roll back against claimed lease: posts=%d media=%d lease=%+v", postCount, mediaCount, stored)
	}
}

func TestPostMediaCreateLockWinsAgainstGCSkipLockedIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	mediaID := uuid.NewString()
	pendingUpload, mediaURL := newTestPostMediaUpload(t, fixture.Author.ID, mediaID, postmediaupload.StatusUploaded)
	now := time.Now().UTC().Truncate(time.Microsecond)
	uploadedAt := now.Add(-25 * time.Hour)
	pendingUpload.UploadedAt = &uploadedAt
	pendingUpload.CleanupAfter = now.Add(-time.Hour)
	if err := db.Create(&pendingUpload).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}) })

	previousDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previousDB })
	originalLock := lockPendingPostMediaUploads
	locked := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseCreate := func() { releaseOnce.Do(func() { close(release) }) }
	lockPendingPostMediaUploads = func(tx *gorm.DB, ids []string) ([]models.PostMediaUpload, error) {
		uploads, err := originalLock(tx, ids)
		if err == nil {
			locked <- struct{}{}
			<-release
		}
		return uploads, err
	}
	t.Cleanup(func() { lockPendingPostMediaUploads = originalLock })
	t.Cleanup(releaseCreate)

	type createResult struct {
		post models.Post
		err  error
	}
	result := make(chan createResult, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		var post models.Post
		media := []validatedPostMedia{{MediaID: mediaID, MediaType: "image", PublicURL: mediaURL}}
		err := persistPostGraph(context.Background(), &post, fixture.Author.ID, "create locked before gc", createPostRequest{Content: "create locked before gc"}, media, now)
		result <- createResult{post: post, err: err}
	}()
	t.Cleanup(func() {
		releaseCreate()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("create goroutine did not stop during test cleanup")
		}
	})
	select {
	case <-locked:
	case <-time.After(5 * time.Second):
		t.Fatal("create did not acquire the uploaded-media row lock")
	}
	claims, err := postmediaupload.ClaimCleanupBatch(context.Background(), db, now, 15*time.Minute, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("GC did not skip row locked by create: %+v", claims)
	}
	releaseCreate()
	var created createResult
	select {
	case created = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("create did not finish after releasing the row lock")
	}
	if created.err != nil {
		t.Fatalf("create failed while winning the row lock: %v", created.err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", created.post.ID).Delete(&models.PostMedia{})
		db.Unscoped().Delete(&models.Post{}, created.post.ID)
	})
	var boundCount, pendingCount int64
	if err := db.Model(&models.PostMedia{}).Where("post_id = ?", created.post.ID).Count(&boundCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.PostMediaUpload{}).Where("media_id = ?", mediaID).Count(&pendingCount).Error; err != nil {
		t.Fatal(err)
	}
	if boundCount != 1 || pendingCount != 0 {
		t.Fatalf("create lock winner state bound=%d pending=%d", boundCount, pendingCount)
	}
}

func TestPersistPostGraphRejectsMissingWrongOwnerUploadingAndStaleMediaIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	cases := []struct {
		name      string
		rowOwner  uint
		status    string
		createRow bool
		staleURL  bool
	}{
		{name: "missing", createRow: false},
		{name: "wrong owner", rowOwner: fixture.Other.ID, status: postmediaupload.StatusUploaded, createRow: true},
		{name: "uploading", rowOwner: fixture.Author.ID, status: postmediaupload.StatusUploading, createRow: true},
		{name: "stale variant URL", rowOwner: fixture.Author.ID, status: postmediaupload.StatusUploaded, createRow: true, staleURL: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mediaID := uuid.NewString()
			urlOwner := fixture.Author.ID
			rowOwner := testCase.rowOwner
			if rowOwner == 0 {
				rowOwner = fixture.Author.ID
			}
			pending, rowURL := newTestPostMediaUpload(t, rowOwner, mediaID, testCase.status)
			requestedURL := rowURL
			if rowOwner != urlOwner || testCase.staleURL {
				requestPaths, err := postmedia.BuildUserV1ObjectPaths(urlOwner, mediaID, ".jpg", ".png")
				if err != nil {
					t.Fatal(err)
				}
				requestedURL = postmedia.PublicURL(requestPaths.MediumObjectKey)
			}
			if testCase.createRow {
				if err := db.Create(&pending).Error; err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Unscoped().Where("media_id = ?", mediaID).Delete(&models.PostMediaUpload{}) })
			}
			var post models.Post
			media := []validatedPostMedia{{MediaID: mediaID, MediaType: "image", PublicURL: requestedURL}}
			err := persistPostGraph(context.Background(), &post, fixture.Author.ID, "invalid media transaction", createPostRequest{Content: "invalid media transaction"}, media, time.Now().UTC())
			if err != errInvalidPostMedia {
				t.Fatalf("persist error=%v want invalid media", err)
			}
			var postCount, pendingCount int64
			if err := db.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Count(&postCount).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.PostMediaUpload{}).Where("media_id = ?", mediaID).Count(&pendingCount).Error; err != nil {
				t.Fatal(err)
			}
			wantPending := int64(0)
			if testCase.createRow {
				wantPending = 1
			}
			if postCount != 0 || pendingCount != wantPending {
				t.Fatalf("posts=%d pending=%d want pending=%d", postCount, pendingCount, wantPending)
			}
		})
	}
}

func TestPersistPostGraphCompetingReverseMediaOrderLocksDeterministicallyIntegration(t *testing.T) {
	db := openReplyIntegrationDatabase(t)
	fixture := newReplyIntegrationFixture(t, db)
	mediaIDs := []string{uuid.NewString(), uuid.NewString()}
	sort.Strings(mediaIDs)
	mediaByID := make(map[string]validatedPostMedia, len(mediaIDs))
	for _, mediaID := range mediaIDs {
		pending, mediaURL := newTestPostMediaUpload(t, fixture.Author.ID, mediaID, postmediaupload.StatusUploaded)
		if err := db.Create(&pending).Error; err != nil {
			t.Fatal(err)
		}
		mediaByID[mediaID] = validatedPostMedia{MediaID: mediaID, MediaType: "image", PublicURL: mediaURL}
	}
	t.Cleanup(func() { db.Unscoped().Where("media_id IN ?", mediaIDs).Delete(&models.PostMediaUpload{}) })

	type result struct {
		postID uint
		err    error
	}
	results := make(chan result, 2)
	readyAtLock := make(chan struct{}, 2)
	releaseLocks := make(chan struct{})
	originalLock := lockPendingPostMediaUploads
	lockPendingPostMediaUploads = func(tx *gorm.DB, ids []string) ([]models.PostMediaUpload, error) {
		readyAtLock <- struct{}{}
		<-releaseLocks
		return originalLock(tx, ids)
	}
	t.Cleanup(func() { lockPendingPostMediaUploads = originalLock })
	var waitGroup sync.WaitGroup
	orders := [][]string{mediaIDs, {mediaIDs[1], mediaIDs[0]}}
	for index, order := range orders {
		media := []validatedPostMedia{mediaByID[order[0]], mediaByID[order[1]]}
		waitGroup.Add(1)
		go func(index int, media []validatedPostMedia) {
			defer waitGroup.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var post models.Post
			err := persistPostGraph(ctx, &post, fixture.Author.ID, "reverse media order", createPostRequest{Content: "reverse media order"}, media, time.Now().UTC())
			results <- result{postID: post.ID, err: err}
		}(index, media)
	}
	for range 2 {
		select {
		case <-readyAtLock:
		case <-time.After(5 * time.Second):
			close(releaseLocks)
			waitGroup.Wait()
			t.Fatal("both competing transactions did not reach upload locking")
		}
	}
	close(releaseLocks)
	waitGroup.Wait()
	close(results)

	var createdPostIDs []uint
	successCount, unavailableCount := 0, 0
	for result := range results {
		createdPostIDs = append(createdPostIDs, result.postID)
		switch {
		case result.err == nil:
			successCount++
		case errors.Is(result.err, errInvalidPostMedia):
			unavailableCount++
		default:
			t.Fatalf("competing persist error=%v", result.err)
		}
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id IN ?", createdPostIDs).Delete(&models.PostMedia{})
		db.Unscoped().Where("id IN ?", createdPostIDs).Delete(&models.Post{})
	})
	if successCount != 1 || unavailableCount != 1 {
		t.Fatalf("competing transactions success=%d unavailable=%d", successCount, unavailableCount)
	}
	var boundCount, pendingCount int64
	if err := db.Model(&models.PostMedia{}).Where("post_id IN ?", createdPostIDs).Count(&boundCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.PostMediaUpload{}).Where("media_id IN ?", mediaIDs).Count(&pendingCount).Error; err != nil {
		t.Fatal(err)
	}
	if boundCount != 2 || pendingCount != 0 {
		t.Fatalf("competing media state bound=%d pending=%d", boundCount, pendingCount)
	}
}

func newTestPostMediaUpload(t *testing.T, ownerID uint, mediaID, status string) (models.PostMediaUpload, string) {
	t.Helper()
	paths, err := postmedia.BuildUserV1ObjectPaths(ownerID, mediaID, ".jpg", ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	upload := models.PostMediaUpload{
		MediaID: mediaID, OwnerID: ownerID, Status: status,
		OriginalObjectKey: paths.OriginalObjectKey, MediumObjectKey: paths.MediumObjectKey,
		LargeObjectKey: paths.LargeObjectKey, ManifestObjectKey: paths.ManifestObjectKey,
		MediumURL: postmedia.PublicURL(paths.MediumObjectKey), LargeURL: postmedia.PublicURL(paths.LargeObjectKey),
		Width: 1200, Height: 800, CreatedAt: now, CleanupAfter: now.Add(postmediaupload.DefaultUploadTimeout),
	}
	if status == postmediaupload.StatusUploaded {
		uploadedAt := now
		upload.UploadedAt = &uploadedAt
		upload.CleanupAfter = uploadedAt.Add(postmediaupload.DefaultGracePeriod)
	}
	return upload, upload.MediumURL
}

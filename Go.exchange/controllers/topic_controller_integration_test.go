package controllers

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestTopicFeedStableSeededBucketTraversalIntegration(t *testing.T) {
	db := openProfileTimelineIntegrationDB(t)
	if err := db.AutoMigrate(&models.DevDataMirrorAccount{}, &models.DevDataMirrorPost{}); err != nil {
		t.Fatal(err)
	}
	originalDB := global.APIDb
	global.APIDb = db
	t.Cleanup(func() { global.APIDb = originalDB })

	anchor := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	sourceA, sourceB := "topic-a-"+uuid.NewString(), "topic-b-"+uuid.NewString()
	disabledSource := "topic-disabled-" + uuid.NewString()
	topic := config.CuratedTopic{
		Slug:  "topic-seeded-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Label: "Topic", Description: "Seeded pagination fixture",
		SourceKeys: []string{sourceA, sourceB, disabledSource}, Enabled: true,
	}
	originalLoader := loadTopicConfiguration
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) {
		return config.CuratedTopicsConfig{Version: config.CuratedTopicsVersion, Topics: []config.CuratedTopic{topic}}, nil
	}
	t.Cleanup(func() { loadTopicConfiguration = originalLoader })

	var userIDs, accountIDs, postIDs, mappedIDs []uint
	t.Cleanup(func() {
		if len(mappedIDs) != 0 {
			db.Unscoped().Where("local_post_id IN ?", mappedIDs).Delete(&models.DevDataMirrorPost{})
		}
		if len(accountIDs) != 0 {
			db.Unscoped().Where("id IN ?", accountIDs).Delete(&models.DevDataMirrorAccount{})
		}
		if len(postIDs) != 0 {
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		if len(userIDs) != 0 {
			db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
		}
	})
	newUser := func(label string) models.User {
		t.Helper()
		user := models.User{Username: "topic-" + label + "-" + uuid.NewString(), Password: "test"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		userIDs = append(userIDs, user.ID)
		return user
	}
	newAccount := func(key string, enabled bool) models.DevDataMirrorAccount {
		t.Helper()
		owner := newUser("account")
		account := models.DevDataMirrorAccount{
			RegistryKey: key, Platform: "x", SourceUserID: uuid.NewString(), SourceHandle: key,
			LocalUserID: owner.ID, Category: "test", Enabled: enabled,
		}
		if err := db.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		accountIDs = append(accountIDs, account.ID)
		return account
	}
	newPost := func(author models.User, label string, createdAt time.Time, visibility string) models.Post {
		t.Helper()
		post := models.Post{
			Model:    gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt},
			AuthorID: author.ID, Content: label, Visibility: visibility,
		}
		if err := db.Create(&post).Error; err != nil {
			t.Fatal(err)
		}
		postIDs = append(postIDs, post.ID)
		return post
	}
	mapPost := func(account models.DevDataMirrorAccount, post models.Post, sourceAt, importedAt time.Time, state string) {
		t.Helper()
		mapping := models.DevDataMirrorPost{
			Platform: "x", SourcePostID: uuid.NewString(), SourceURL: "https://example.test/source",
			MirrorAccountID: account.ID, LocalPostID: post.ID, SourceCreatedAt: sourceAt,
			ContentHash: "topic-fixture", State: state, ImportedAt: importedAt,
			CreatedAt: importedAt, UpdatedAt: importedAt,
		}
		if err := db.Create(&mapping).Error; err != nil {
			t.Fatal(err)
		}
		mappedIDs = append(mappedIDs, post.ID)
	}

	accountA := newAccount(sourceA, true)
	accountB := newAccount(sourceB, true)
	disabledAccount := newAccount(disabledSource, true)
	disabledResult := db.Model(&models.DevDataMirrorAccount{}).
		Where("id = ?", disabledAccount.ID).
		UpdateColumn("enabled", false)
	if disabledResult.Error != nil {
		t.Fatal(disabledResult.Error)
	}
	if disabledResult.RowsAffected != 1 {
		t.Fatalf("disabled account update affected %d rows, want 1", disabledResult.RowsAffected)
	}
	outsideAccount := newAccount("topic-outside-"+uuid.NewString(), true)
	author, deletedAuthor := newUser("author"), newUser("deleted-author")
	eligible := make(map[uint]time.Time)
	for index := 0; index < 9; index++ {
		at := anchor.Add(-time.Duration(index+1) * time.Minute)
		post := newPost(author, "fresh-"+strconv.Itoa(index), at, "public")
		account := accountA
		if index%2 == 1 {
			account = accountB
		}
		mapPost(account, post, at, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
		eligible[post.ID] = at
	}
	for index, hours := range []int{13, 14, 15, 25, 26} {
		at := anchor.Add(-time.Duration(hours) * time.Hour)
		post := newPost(author, "older-"+strconv.Itoa(index), at, "public")
		account := accountA
		if index >= 3 {
			account = accountB
		}
		mapPost(account, post, at, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
		eligible[post.ID] = at
	}
	excludedAt := anchor.Add(-10 * time.Minute)
	disabled := newPost(author, "disabled", excludedAt, "public")
	mapPost(disabledAccount, disabled, excludedAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
	outside := newPost(author, "outside", excludedAt, "public")
	mapPost(outsideAccount, outside, excludedAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
	tombstone := newPost(author, "tombstone", excludedAt, "public")
	mapPost(accountA, tombstone, excludedAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateTombstone)
	private := newPost(author, "private", excludedAt, "private")
	mapPost(accountA, private, excludedAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
	deleted := newPost(author, "deleted", excludedAt, "public")
	mapPost(accountA, deleted, excludedAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	deletedAuthorPost := newPost(deletedAuthor, "deleted-author", excludedAt, "public")
	mapPost(accountA, deletedAuthorPost, excludedAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	hash, err := topicCursorCriteriaHash(topic)
	if err != nil {
		t.Fatal(err)
	}
	collect := func(initial topicTraversal, limit int) [][]uint {
		t.Helper()
		pages := make([][]uint, 0)
		traversal := initial
		for pageNo := 0; pageNo < 100; pageNo++ {
			page, err := loadTopicPostsPageWithTraversal(context.Background(), db, topic, limit, traversal)
			if err != nil {
				t.Fatalf("load page %d: %v", pageNo+1, err)
			}
			pages = append(pages, topicPostIDs(page.Items))
			if page.NextCursor == nil {
				return pages
			}
			cursor, err := decodeTopicPostCursor(*page.NextCursor, hash)
			if err != nil {
				t.Fatalf("decode cursor: %v", err)
			}
			traversal, err = resolveTopicTraversal(topic, &cursor)
			if err != nil {
				t.Fatalf("resolve cursor: %v", err)
			}
		}
		t.Fatal("topic traversal did not terminate")
		return nil
	}
	initial := topicTraversal{TopicHash: hash, AnchorAt: anchor, Seed: 1}
	pages := collect(initial, 4)
	repeated := collect(initial, 4)
	otherSeed := collect(topicTraversal{TopicHash: hash, AnchorAt: anchor, Seed: 2}, 4)
	flatten := func(values [][]uint) []uint {
		ids := make([]uint, 0)
		for _, page := range values {
			ids = append(ids, page...)
		}
		return ids
	}
	ids := flatten(pages)
	if !reflect.DeepEqual(ids, flatten(repeated)) || !reflect.DeepEqual(pages, repeated) {
		t.Fatalf("same anchor/seed changed order or page boundaries: %v vs %v", pages, repeated)
	}
	if len(pages) < 3 || len(pages[2]) != 4 {
		t.Fatalf("expected a page crossing the bucket boundary, got %v", pages)
	}
	seen := make(map[uint]struct{}, len(ids))
	lastBucket := int64(-1)
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate post %d", id)
		}
		seen[id] = struct{}{}
		at, exists := eligible[id]
		if !exists {
			t.Fatalf("ineligible post %d appeared", id)
		}
		bucket, err := topicBucketIndex(anchor, at)
		if err != nil {
			t.Fatal(err)
		}
		if bucket < lastBucket {
			t.Fatalf("freshness order regressed: bucket %d after %d", bucket, lastBucket)
		}
		lastBucket = bucket
	}
	if len(seen) != len(eligible) {
		t.Fatalf("received %d posts, want %d eligible posts: %v", len(seen), len(eligible), ids)
	}
	for id := range eligible {
		if _, ok := seen[id]; !ok {
			t.Errorf("eligible post %d omitted", id)
		}
	}
	for _, id := range []uint{disabled.ID, outside.ID, tombstone.ID, private.ID, deleted.ID, deletedAuthorPost.ID} {
		if _, ok := seen[id]; ok {
			t.Errorf("excluded post %d appeared in traversal", id)
		}
	}
	bucketZeroOrder := func(values [][]uint) []uint {
		out := make([]uint, 0)
		for _, id := range flatten(values) {
			if bucket, err := topicBucketIndex(anchor, eligible[id]); err == nil && bucket == 0 {
				out = append(out, id)
			}
		}
		return out
	}
	if reflect.DeepEqual(bucketZeroOrder(pages), bucketZeroOrder(otherSeed)) {
		t.Fatalf("fixed seeds 1 and 2 produced equal bucket-0 order: %v", bucketZeroOrder(pages))
	}

	firstPage, err := loadTopicPostsPageWithTraversal(context.Background(), db, topic, 4, initial)
	if err != nil || firstPage.NextCursor == nil {
		t.Fatalf("initial page=%+v err=%v", firstPage, err)
	}
	firstCursor, err := decodeTopicPostCursor(*firstPage.NextCursor, hash)
	if err != nil {
		t.Fatal(err)
	}
	lateAt := anchor.Add(-30 * time.Minute)
	latePost := newPost(author, "late-historical-import", lateAt, "public")
	mapPost(accountA, latePost, lateAt, anchor.Add(time.Minute), models.DevDataMirrorPostStateActive)
	continued, err := resolveTopicTraversal(topic, &firstCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range flatten(collect(continued, 4)) {
		if id == latePost.ID {
			t.Fatal("late historical import entered an existing traversal")
		}
	}
	newPage, err := loadTopicPostsPageWithTraversal(context.Background(), db, topic, 50, topicTraversal{
		TopicHash: hash, AnchorAt: anchor.Add(2 * time.Minute), Seed: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundLate := false
	for _, post := range newPage.Items {
		foundLate = foundLate || post.ID == latePost.ID
	}
	if !foundLate {
		t.Fatal("new traversal did not include the imported historical post")
	}

	t.Run("hydration loss keeps remaining candidates reachable", func(t *testing.T) {
		lossSource := "topic-hydration-loss-" + uuid.NewString()
		lossTopic := config.CuratedTopic{
			Slug:       "topic-hydration-loss-" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			SourceKeys: []string{lossSource}, Enabled: true,
		}
		lossAccount := newAccount(lossSource, true)
		lossAt := anchor.Add(-20 * time.Minute)
		for index := 0; index < 3; index++ {
			post := newPost(author, "hydration-loss-"+strconv.Itoa(index), lossAt, "public")
			mapPost(lossAccount, post, lossAt, anchor.Add(-time.Second), models.DevDataMirrorPostStateActive)
		}
		lossHash, err := topicCursorCriteriaHash(lossTopic)
		if err != nil {
			t.Fatal(err)
		}
		traversal := topicTraversal{TopicHash: lossHash, AnchorAt: anchor, Seed: 19}
		candidates, err := loadTopicTraversalCandidates(db, lossTopic, 3, traversal)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates) != 3 {
			t.Fatalf("candidate count=%d, want 3", len(candidates))
		}

		page, err := loadTopicPostsPageWithTraversalAndHydrator(
			context.Background(), db, lossTopic, 2, traversal,
			func(*gorm.DB) ([]postResponse, error) { return []postResponse{}, nil },
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 0 || page.NextCursor == nil {
			t.Fatalf("page items=%v next_cursor=%v, want empty items and continuation", topicPostIDs(page.Items), page.NextCursor)
		}
		cursor, err := decodeTopicPostCursor(*page.NextCursor, lossHash)
		if err != nil {
			t.Fatal(err)
		}
		if cursor.PostID != candidates[1].ID {
			t.Fatalf("fallback cursor post=%d, want last selected candidate %d (not lookahead %d)", cursor.PostID, candidates[1].ID, candidates[2].ID)
		}

		continued, err := resolveTopicTraversal(lossTopic, &cursor)
		if err != nil {
			t.Fatal(err)
		}
		nextPage, err := loadTopicPostsPageWithTraversal(context.Background(), db, lossTopic, 2, continued)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range topicPostIDs(nextPage.Items) {
			if id == candidates[2].ID {
				return
			}
		}
		t.Fatalf("candidate after fallback cursor %d was not reachable; next page=%v", cursor.PostID, topicPostIDs(nextPage.Items))
	})

	changed := topic
	changed.SourceKeys = append(append([]string(nil), topic.SourceKeys...), "topic-source-c-"+uuid.NewString())
	originalPageLoader := loadTopicPostsPage
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) {
		return config.CuratedTopicsConfig{Version: config.CuratedTopicsVersion, Topics: []config.CuratedTopic{changed}}, nil
	}
	loadTopicPostsPage = func(context.Context, config.CuratedTopic, int, *topicPostCursorV2) (postPageResponse, error) {
		t.Fatal("loader ran for a cursor bound to changed configuration")
		return postPageResponse{}, nil
	}
	t.Cleanup(func() { loadTopicPostsPage = originalPageLoader })
	ctx, recorder := newTopicControllerContext("/api/topics/"+topic.Slug+"/posts?limit=4&cursor="+*firstPage.NextCursor, topic.Slug)
	GetTopicPosts(ctx)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "\"error\":\"invalid cursor\"") {
		t.Fatalf("changed SourceKeys status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func topicPostIDs(posts []postResponse) []uint {
	ids := make([]uint, 0, len(posts))
	for _, post := range posts {
		ids = append(ids, post.ID)
	}
	return ids
}

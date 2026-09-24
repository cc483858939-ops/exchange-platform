package controllers

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestTopicFeedMembershipOrderingAndCursorIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db := openRecommendationCandidateIntegrationDB(t)
	if err := db.AutoMigrate(&models.DevDataMirrorAccount{}, &models.DevDataMirrorPost{}); err != nil {
		t.Fatal(err)
	}

	originalLoader := loadTopicConfiguration
	loadTopicConfiguration = func() (config.CuratedTopicsConfig, error) {
		return config.CuratedTopicsConfig{
			Version: config.CuratedTopicsVersion,
			Topics: []config.CuratedTopic{{
				Slug: "topic-one", Label: "Topic One", Description: "A test topic",
				SourceKeys: []string{"included", "disabled"}, Enabled: true,
			}},
		}, nil
	}
	t.Cleanup(func() { loadTopicConfiguration = originalLoader })

	newUser := func(label string) models.User {
		t.Helper()
		user := models.User{Username: "topic-feed-" + label + "-" + uuid.NewString(), Password: "test"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		return user
	}
	newAccount := func(key string, enabled bool) models.DevDataMirrorAccount {
		t.Helper()
		user := newUser("account-" + key)
		account := models.DevDataMirrorAccount{
			RegistryKey: key, Platform: "x", SourceUserID: uuid.NewString(), SourceHandle: key,
			LocalUserID: user.ID, Category: "test", Enabled: enabled,
		}
		if err := db.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		return account
	}
	newPost := func(author models.User, label string, createdAt time.Time) models.Post {
		t.Helper()
		post := models.Post{Model: gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt}, AuthorID: author.ID, Content: label, Visibility: "public"}
		if err := db.Create(&post).Error; err != nil {
			t.Fatal(err)
		}
		return post
	}
	mapPost := func(account models.DevDataMirrorAccount, post models.Post, state string) {
		t.Helper()
		mapping := models.DevDataMirrorPost{
			Platform: "x", SourcePostID: uuid.NewString(), SourceURL: "https://example.test/source",
			MirrorAccountID: account.ID, LocalPostID: post.ID, SourceCreatedAt: post.CreatedAt,
			ContentHash: "test-hash", State: state, ImportedAt: post.CreatedAt,
			CreatedAt: post.CreatedAt, UpdatedAt: post.CreatedAt,
		}
		if err := db.Create(&mapping).Error; err != nil {
			t.Fatal(err)
		}
	}

	includedAccount := newAccount("included", true)
	disabledAccount := newAccount("disabled", false)
	otherAccount := newAccount("other", true)
	author := newUser("author")
	deletedAuthor := newUser("deleted-author")
	baseTime := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	older := newPost(author, "older", baseTime.Add(-time.Hour))
	tieLow := newPost(author, "tie-low", baseTime)
	tieHigh := newPost(author, "tie-high", baseTime)
	tombstoned := newPost(author, "tombstoned", baseTime.Add(2*time.Hour))
	deleted := newPost(author, "deleted", baseTime.Add(3*time.Hour))
	deletedAuthorPost := newPost(deletedAuthor, "deleted-author", baseTime.Add(4*time.Hour))
	disabled := newPost(author, "disabled-account", baseTime.Add(5*time.Hour))
	unrelated := newPost(author, "unrelated-account", baseTime.Add(6*time.Hour))
	mapPost(includedAccount, older, models.DevDataMirrorPostStateActive)
	mapPost(includedAccount, tieLow, models.DevDataMirrorPostStateActive)
	mapPost(includedAccount, tieHigh, models.DevDataMirrorPostStateActive)
	mapPost(includedAccount, tombstoned, models.DevDataMirrorPostStateTombstone)
	mapPost(includedAccount, deleted, models.DevDataMirrorPostStateActive)
	mapPost(includedAccount, deletedAuthorPost, models.DevDataMirrorPostStateActive)
	mapPost(disabledAccount, disabled, models.DevDataMirrorPostStateActive)
	mapPost(otherAccount, unrelated, models.DevDataMirrorPostStateActive)
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	accountIDs := []uint{includedAccount.ID, disabledAccount.ID, otherAccount.ID}
	postIDs := []uint{older.ID, tieLow.ID, tieHigh.ID, tombstoned.ID, deleted.ID, deletedAuthorPost.ID, disabled.ID, unrelated.ID}
	userIDs := []uint{includedAccount.LocalUserID, disabledAccount.LocalUserID, otherAccount.LocalUserID, author.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("mirror_account_id IN ?", accountIDs).Delete(&models.DevDataMirrorPost{})
		db.Unscoped().Where("id IN ?", accountIDs).Delete(&models.DevDataMirrorAccount{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	ctx, recorder := newTopicControllerContext("/api/topics/topic-one/posts?limit=2", "topic-one")
	GetTopicPosts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var first topicPostsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].ID != tieHigh.ID || first.Items[1].ID != tieLow.ID || first.NextCursor == nil {
		t.Fatalf("first page IDs/cursor=%v/%v, want [%d %d] and next cursor", topicPostIDs(first.Items), first.NextCursor, tieHigh.ID, tieLow.ID)
	}
	cursor, err := decodeTopicPostCursor(*first.NextCursor)
	if err != nil {
		t.Fatalf("decode first-page cursor: %v", err)
	}
	if !cursor.CreatedAt.Equal(tieLow.CreatedAt) || cursor.PostID != tieLow.ID {
		t.Fatalf("first-page cursor=%+v, want query ordering tuple (%s, %d)", cursor, tieLow.CreatedAt, tieLow.ID)
	}

	ctx, recorder = newTopicControllerContext("/api/topics/topic-one/posts?limit=2&cursor="+*first.NextCursor, "topic-one")
	GetTopicPosts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var second topicPostsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != older.ID || second.NextCursor != nil {
		t.Fatalf("second page IDs/cursor=%v/%v, want [%d] and no cursor", topicPostIDs(second.Items), second.NextCursor, older.ID)
	}
	for _, firstPost := range first.Items {
		for _, secondPost := range second.Items {
			if firstPost.ID == secondPost.ID {
				t.Fatalf("post %d repeated on page 2", firstPost.ID)
			}
		}
	}
}

func topicPostIDs(posts []postResponse) []uint {
	ids := make([]uint, 0, len(posts))
	for _, post := range posts {
		ids = append(ids, post.ID)
	}
	return ids
}

package controllers

import (
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestMentionSuggestionsPrioritizeRecentAndFilterBeforeLimitIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.UserMute{}, &models.UserMentionRecent{}); err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil && err != gorm.ErrInvalidTransaction {
			t.Errorf("rollback mention suggestions transaction: %v", err)
		}
	})

	token := uuid.NewString()
	makeUser := func(username string) models.User {
		t.Helper()
		user := models.User{Username: username + "-" + token, DisplayName: username}
		if err := tx.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		return user
	}
	viewer := makeUser("mention-viewer")
	recent := make([]models.User, 5)
	now := time.Now().UTC()
	for i := range recent {
		recent[i] = makeUser(fmt.Sprintf("recent-%d", i))
		if err := tx.Create(&models.UserMentionRecent{UserID: viewer.ID, MentionedUserID: recent[i].ID, LastMentionedAt: now.Add(-time.Duration(i+1) * time.Minute)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	mutedRecent := makeUser("muted-recent")
	if err := tx.Create(&models.UserMentionRecent{UserID: viewer.ID, MentionedUserID: mutedRecent.ID, LastMentionedAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	deletedRecent := makeUser("deleted-recent")
	if err := tx.Create(&models.UserMentionRecent{UserID: viewer.ID, MentionedUserID: deletedRecent.ID, LastMentionedAt: now.Add(2 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete(&deletedRecent).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Create(&models.UserMute{MuterID: viewer.ID, MutedID: mutedRecent.ID}).Error; err != nil {
		t.Fatal(err)
	}
	following := make([]models.User, 5)
	for i := range following {
		following[i] = makeUser(fmt.Sprintf("following-%d", i))
		if err := tx.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: following[i].ID, CreatedAt: now.Add(-time.Duration(i+1) * time.Minute)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// This overlap must keep the recent reason and not consume a second slot.
	if err := tx.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: recent[0].ID, CreatedAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: recent[4].ID, CreatedAt: now.Add(2 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	prefixRecent := makeUser("prefix-recent")
	if err := tx.Create(&models.UserMentionRecent{UserID: viewer.ID, MentionedUserID: prefixRecent.ID, LastMentionedAt: now.Add(-24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	prefixFollowing := makeUser("prefix-following")
	if err := tx.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: prefixFollowing.ID, CreatedAt: now.Add(-24 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}

	items, err := loadMentionSuggestions(tx, viewer.ID, "", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 8 {
		t.Fatalf("suggestion count=%d want=8: %+v", len(items), items)
	}
	for i := 0; i < 4; i++ {
		if items[i].User.ID != recent[i].ID || items[i].Reason != "recently_mentioned" {
			t.Fatalf("recent position %d=%+v want user %d recently_mentioned", i, items[i], recent[i].ID)
		}
	}
	for i := 0; i < 4; i++ {
		if items[4+i].User.ID != following[i].ID || items[4+i].Reason != "following" {
			t.Fatalf("following position %d=%+v want user %d following", i, items[4+i], following[i].ID)
		}
	}
	for _, item := range items {
		if item.User.ID == viewer.ID || item.User.ID == mutedRecent.ID || item.User.ID == deletedRecent.ID {
			t.Fatalf("ineligible user returned: %+v", item)
		}
	}

	prefixItems, err := loadMentionSuggestions(tx, viewer.ID, "PREFIX", 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixItems) != 2 || prefixItems[0].User.ID != prefixRecent.ID || prefixItems[0].Reason != "recently_mentioned" ||
		prefixItems[1].User.ID != prefixFollowing.ID || prefixItems[1].Reason != "following" {
		t.Fatalf("case-insensitive prefix results=%+v", prefixItems)
	}
}

package tasks

import (
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/internal/testdb"
	"Go.exchange/models"
	"gorm.io/gorm"
)

func TestNotificationParticipantsUseOneQueryAndRequireBothRolesIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := fmt.Sprintf("notification_participants_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}); err != nil {
		t.Fatal(err)
	}
	for id := uint(1); id <= 3; id++ {
		user := models.User{Model: gorm.Model{ID: id}, Username: fmt.Sprintf("participant-%d", id), Password: "test"}
		if err := tx.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Delete(&models.User{}, 3).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	name := schema + "_count"
	if err := tx.Callback().Query().After("gorm:query").Register(name, func(query *gorm.DB) {
		if query.Statement.Table == "users" {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Callback().Query().Remove(name) })
	candidates := []models.Notification{
		{RecipientID: 1, ActorID: 2, Type: models.NotificationTypeUserFollowed, SourceVersion: 1},
		{RecipientID: 2, ActorID: 1, Type: models.NotificationTypeUserFollowed, SourceVersion: 1},
		{RecipientID: 1, ActorID: 3, Type: models.NotificationTypeUserFollowed, SourceVersion: 1},
		{RecipientID: 3, ActorID: 1, Type: models.NotificationTypeUserFollowed, SourceVersion: 1},
		{RecipientID: 1, ActorID: 99, Type: models.NotificationTypeUserFollowed, SourceVersion: 1},
		{RecipientID: 99, ActorID: 1, Type: models.NotificationTypeUserFollowed, SourceVersion: 1},
	}
	filtered, err := filterNotificationCandidates(tx, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 || len(filtered) != 2 {
		t.Fatalf("queries=%d candidates=%+v", queries, filtered)
	}
	if filtered[0].RecipientID != 1 || filtered[1].RecipientID != 2 {
		t.Fatalf("valid reciprocal roles lost: %+v", filtered)
	}
}

package controllers

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostTimelineQueriesUseLimitWithoutOffsetIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}, &models.Post{}, &models.PostRepost{}); err != nil {
		t.Fatal(err)
	}
	queryLogger := &postDetailSQLLogger{Interface: logger.Default}
	originalDB := global.Db
	global.Db = db.Session(&gorm.Session{Logger: queryLogger})
	t.Cleanup(func() { global.Db = originalDB })

	viewer := models.User{Username: "timeline-sql-viewer-" + uuid.NewString(), Password: "test"}
	target := models.User{Username: "timeline-sql-target-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: target.ID}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("follower_id = ? OR following_id = ?", viewer.ID, target.ID).Delete(&models.UserFollow{})
		db.Unscoped().Where("id IN ?", []uint{viewer.ID, target.ID}).Delete(&models.User{})
	})

	_, status, body := requestFollowingTimeline(t, viewer.ID, "limit=20")
	if status != http.StatusOK {
		t.Fatalf("following status=%d body=%s", status, body)
	}
	ctx, recorder := newUserControllerContext("/api/users/"+strconvUint(target.ID)+"/posts?limit=20", strconvUint(target.ID))
	GetUserPosts(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("user posts status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	postQueries := 0
	for _, query := range queryLogger.snapshot() {
		normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
		if !strings.Contains(normalized, " from ") || !strings.Contains(normalized, "posts") || !strings.Contains(normalized, " limit 21") {
			continue
		}
		postQueries++
		if strings.Contains(normalized, " offset ") {
			t.Fatalf("article timeline query used OFFSET: %s", query)
		}
	}
	if postQueries < 2 {
		t.Fatalf("expected bounded Article queries for Following and User Articles, got %d queries=%v", postQueries, queryLogger.snapshot())
	}
}

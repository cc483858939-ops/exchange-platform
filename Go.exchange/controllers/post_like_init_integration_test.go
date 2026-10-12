package controllers

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/global"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"
)

func TestPostCreateRollsBackWhenLikeInitEnqueueFailsIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
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
	schema := fmt.Sprintf("post_init_enqueue_failure_%d", time.Now().UnixNano())
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.AutoMigrate(&models.User{}, &models.Post{}); err != nil {
		t.Fatal(err)
	}
	originalDB, originalConfig := global.APIDb, config.AppConfig
	global.APIDb = tx
	config.AppConfig = nil
	t.Cleanup(func() {
		global.APIDb = originalDB
		config.AppConfig = originalConfig
	})
	user := models.User{Username: fmt.Sprintf("enqueue-failure-%d", time.Now().UnixNano()), Password: "test"}
	if err := tx.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	var post models.Post
	err = persistPostGraph(context.Background(), &post, user.ID, "atomic task enqueue", createPostRequest{Content: "atomic task enqueue"}, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("Post creation succeeded without the required durable initialization table")
	}
	var posts int64
	if err := tx.Model(&models.Post{}).Count(&posts).Error; err != nil {
		t.Fatal(err)
	}
	if posts != 0 {
		t.Fatalf("Post rows=%d after task enqueue failure, want transaction rollback", posts)
	}
}

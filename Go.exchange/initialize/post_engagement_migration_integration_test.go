package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostEngagementMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })
	if err := RunMigrations(); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(); err != nil {
		t.Fatal(err)
	}

	var column struct {
		Nullable string `gorm:"column:is_nullable"`
		Default  string `gorm:"column:column_default"`
	}
	if err := db.Raw(`
SELECT is_nullable, COALESCE(column_default, '') AS column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'posts'
  AND column_name = 'reply_count'
`).Scan(&column).Error; err != nil {
		t.Fatal(err)
	}
	if column.Nullable != "NO" || !strings.Contains(column.Default, "0") {
		t.Fatalf("posts.reply_count nullable=%q default=%q", column.Nullable, column.Default)
	}
	var languageColumn struct {
		Nullable string `gorm:"column:is_nullable"`
		Default  string `gorm:"column:column_default"`
	}
	if err := db.Raw(`
SELECT is_nullable, COALESCE(column_default, '') AS column_default
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'posts'
  AND column_name = 'language'
`).Scan(&languageColumn).Error; err != nil {
		t.Fatal(err)
	}
	if languageColumn.Nullable != "NO" || !strings.Contains(strings.ToLower(languageColumn.Default), "und") {
		t.Fatalf("posts.language nullable=%q default=%q", languageColumn.Nullable, languageColumn.Default)
	}
	var languageConstraint string
	if err := db.Raw(`
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'posts'::regclass
  AND conname = 'chk_posts_language_supported'
`).Scan(&languageConstraint).Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"zh", "ja", "en", "und"} {
		if !strings.Contains(strings.ToLower(languageConstraint), value) {
			t.Fatalf("language constraint=%q missing %q", languageConstraint, value)
		}
	}
	var viewColumn struct {
		Nullable string `gorm:"column:is_nullable"`
		Default  string `gorm:"column:column_default"`
	}
	if err := db.Raw("SELECT is_nullable, COALESCE(column_default, '') AS column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'posts' AND column_name = 'view_count'").Scan(&viewColumn).Error; err != nil {
		t.Fatal(err)
	}
	if viewColumn.Nullable != "NO" || !strings.Contains(viewColumn.Default, "0") {
		t.Fatalf("posts.view_count nullable=%q default=%q", viewColumn.Nullable, viewColumn.Default)
	}
	if !db.Migrator().HasTable(&models.PostRepost{}) {
		t.Fatal("post_reposts table does not exist")
	}
	var repostIndexes []struct {
		Name string `gorm:"column:indexname"`
	}
	if err := db.Raw(`
SELECT indexname
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'post_reposts'
  AND indexname IN (
    'uidx_post_reposts_user_post',
    'idx_post_reposts_user_created',
    'idx_post_reposts_post'
  )
`).Scan(&repostIndexes).Error; err != nil {
		t.Fatal(err)
	}
	if len(repostIndexes) != 3 {
		t.Fatalf("article repost indexes=%#v", repostIndexes)
	}

	var definition string
	if err := db.Raw(`
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'posts'::regclass
  AND conname = 'chk_posts_reply_count_nonnegative'
`).Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	normalizedDefinition := strings.ToLower(definition)
	if !strings.Contains(normalizedDefinition, "reply_count") ||
		!strings.Contains(normalizedDefinition, ">=") ||
		!strings.Contains(normalizedDefinition, "0") {
		t.Fatalf("comment count check definition=%q", definition)
	}

	user := models.User{Username: "engagement-migration-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Post{AuthorID: user.ID, Content: "engagement migration", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	if article.Language != "und" {
		t.Fatalf("default Post language=%q want und", article.Language)
	}
	validLanguages := []string{"zh", "ja", "en", "und"}
	validPostIDs := make([]uint, 0, len(validLanguages))
	for _, language := range validLanguages {
		validPost := models.Post{AuthorID: user.ID, Content: "language migration " + language, Language: language, Visibility: "public"}
		if err := db.Create(&validPost).Error; err != nil {
			t.Fatalf("create valid language %q: %v", language, err)
		}
		validPostIDs = append(validPostIDs, validPost.ID)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostRepost{})
		if len(validPostIDs) > 0 {
			db.Unscoped().Where("id IN ?", validPostIDs).Delete(&models.Post{})
		}
		db.Unscoped().Delete(&article)
		db.Unscoped().Delete(&user)
	})
	for _, language := range []string{"", "fr", "english", "jp"} {
		if err := db.Exec("INSERT INTO posts (author_id, content, language, visibility) VALUES (?, ?, ?, ?)", user.ID, "invalid language "+language, language, "public").Error; err == nil {
			t.Fatalf("database accepted invalid language %q", language)
		}
	}

	repost := models.PostRepost{UserID: user.ID, PostID: article.ID}
	if err := db.Create(&repost).Error; err != nil {
		t.Fatal(err)
	}
	duplicateRepost := models.PostRepost{UserID: user.ID, PostID: article.ID}
	if err := db.Create(&duplicateRepost).Error; err == nil {
		t.Fatal("database accepted duplicate article repost relation")
	}

	if err := db.Model(&article).Update("reply_count", -1).Error; err == nil {
		t.Fatal("database accepted a negative post reply_count")
	}
	if err := db.Model(&article).Update("view_count", -1).Error; err == nil {
		t.Fatal("database accepted a negative article view_count")
	}

	legacyTx := db.Begin()
	if legacyTx.Error != nil {
		t.Fatal(legacyTx.Error)
	}
	defer legacyTx.Rollback()
	legacyUser := models.User{Username: "language-legacy-" + uuid.NewString(), Password: "test"}
	if err := legacyTx.Create(&legacyUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyTx.Exec("ALTER TABLE posts ALTER COLUMN language DROP NOT NULL").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyTx.Exec("ALTER TABLE posts DROP CONSTRAINT chk_posts_language_supported").Error; err != nil {
		t.Fatal(err)
	}
	var legacyPostID uint
	if err := legacyTx.Raw("INSERT INTO posts (author_id, content, language, visibility) VALUES (?, ?, ?, ?) RETURNING id", legacyUser.ID, "legacy blank language", "", "public").Scan(&legacyPostID).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyPostSchemaConstraints(legacyTx); err != nil {
		t.Fatal(err)
	}
	var migratedLanguage string
	if err := legacyTx.Raw("SELECT language FROM posts WHERE id = ?", legacyPostID).Scan(&migratedLanguage).Error; err != nil {
		t.Fatal(err)
	}
	if migratedLanguage != "und" {
		t.Fatalf("legacy blank language migrated to %q want und", migratedLanguage)
	}
}

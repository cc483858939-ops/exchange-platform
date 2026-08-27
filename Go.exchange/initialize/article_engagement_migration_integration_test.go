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

func TestArticleEngagementMigrationIntegration(t *testing.T) {
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
  AND table_name = 'articles'
  AND column_name = 'comment_count'
`).Scan(&column).Error; err != nil {
		t.Fatal(err)
	}
	if column.Nullable != "NO" || !strings.Contains(column.Default, "0") {
		t.Fatalf("articles.comment_count nullable=%q default=%q", column.Nullable, column.Default)
	}
	var viewColumn struct {
		Nullable string `gorm:"column:is_nullable"`
		Default  string `gorm:"column:column_default"`
	}
	if err := db.Raw("SELECT is_nullable, COALESCE(column_default, '') AS column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'articles' AND column_name = 'view_count'").Scan(&viewColumn).Error; err != nil {
		t.Fatal(err)
	}
	if viewColumn.Nullable != "NO" || !strings.Contains(viewColumn.Default, "0") {
		t.Fatalf("articles.view_count nullable=%q default=%q", viewColumn.Nullable, viewColumn.Default)
	}
	if !db.Migrator().HasTable(&models.ArticleRepost{}) {
		t.Fatal("article_reposts table does not exist")
	}
	var repostIndexes []struct {
		Name string `gorm:"column:indexname"`
	}
	if err := db.Raw(`
SELECT indexname
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'article_reposts'
  AND indexname IN (
    'uidx_article_reposts_user_article',
    'idx_article_reposts_user_created',
    'idx_article_reposts_article'
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
WHERE conrelid = 'articles'::regclass
  AND conname = 'chk_articles_comment_count_nonnegative'
`).Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	normalizedDefinition := strings.ToLower(definition)
	if !strings.Contains(normalizedDefinition, "comment_count") ||
		!strings.Contains(normalizedDefinition, ">=") ||
		!strings.Contains(normalizedDefinition, "0") {
		t.Fatalf("comment count check definition=%q", definition)
	}

	user := models.User{Username: "engagement-migration-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Article{AuthorID: user.ID, Title: "engagement migration", Preview: "engagement migration"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("article_id = ?", article.ID).Delete(&models.ArticleRepost{})
		db.Unscoped().Delete(&article)
		db.Unscoped().Delete(&user)
	})

	repost := models.ArticleRepost{UserID: user.ID, ArticleID: article.ID}
	if err := db.Create(&repost).Error; err != nil {
		t.Fatal(err)
	}
	duplicateRepost := models.ArticleRepost{UserID: user.ID, ArticleID: article.ID}
	if err := db.Create(&duplicateRepost).Error; err == nil {
		t.Fatal("database accepted duplicate article repost relation")
	}

	if err := db.Model(&article).Update("comment_count", -1).Error; err == nil {
		t.Fatal("database accepted a negative article comment_count")
	}
	if err := db.Model(&article).Update("view_count", -1).Error; err == nil {
		t.Fatal("database accepted a negative article view_count")
	}
}

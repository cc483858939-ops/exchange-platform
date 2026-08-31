package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostEmbeddingVectorDimensionsMigrationIntegration(t *testing.T) {
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

	var definition string
	if err := db.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'post_embeddings'::regclass AND conname = 'chk_post_embeddings_vector_dimensions'").Scan(&definition).Error; err != nil {
		t.Fatal(err)
	}
	normalized := strings.ToLower(definition)
	if !strings.Contains(normalized, "vector_dims") || !strings.Contains(normalized, "dimensions") {
		t.Fatalf("constraint definition=%q", definition)
	}

	user := models.User{Username: "embedding-dimension-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	article := models.Post{AuthorID: user.ID, Content: "body", Visibility: "public"}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	if err := db.Create(&models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test", Dimensions: 3,
		Embedding: pgvector.NewVector([]float32{1, 2}), ContentHash: "bad",
	}).Error; err == nil {
		t.Fatal("database accepted a vector whose dimensions do not match the declaration")
	}
	if err := db.Create(&models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 2}), ContentHash: "good",
	}).Error; err != nil {
		t.Fatalf("database rejected matching vector dimensions: %v", err)
	}
}

func TestLegacyPostEmbeddingJobCleanupIntegration(t *testing.T) {
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

	if err := db.Exec("CREATE TABLE IF NOT EXISTS article_embedding_jobs (id BIGSERIAL PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(); err != nil {
		t.Fatal(err)
	}

	var exists bool
	if err := db.Raw("SELECT to_regclass('public.article_embedding_jobs') IS NOT NULL").Scan(&exists).Error; err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("article_embedding_jobs still exists after legacy cleanup")
	}
}

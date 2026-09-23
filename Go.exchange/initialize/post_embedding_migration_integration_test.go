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
	var primaryKeyDefinition string
	if err := db.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'post_embeddings'::regclass AND conname = 'post_embeddings_pkey'").Scan(&primaryKeyDefinition).Error; err != nil {
		t.Fatal(err)
	}
	if normalizedPrimaryKey := strings.ToLower(strings.ReplaceAll(primaryKeyDefinition, " ", "")); normalizedPrimaryKey != "primarykey(post_id,version)" {
		t.Fatalf("post embedding primary key definition=%q", primaryKeyDefinition)
	}
	var versionIndexDefinition string
	if err := db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'post_embeddings' AND indexname = 'idx_post_embeddings_version_post'").Scan(&versionIndexDefinition).Error; err != nil {
		t.Fatal(err)
	}
	if versionIndexDefinition == "" || !strings.Contains(strings.ToLower(versionIndexDefinition), "(version, post_id)") {
		t.Fatalf("version-first embedding index definition=%q", versionIndexDefinition)
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
	v1 := models.PostEmbedding{
		PostID: article.ID, Version: "v1", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 2}), ContentHash: "good",
	}
	if err := db.Create(&v1).Error; err != nil {
		t.Fatalf("database rejected matching vector dimensions: %v", err)
	}
	v2 := v1
	v2.Version = "v2"
	v2.Model = "test-v2"
	v2.Embedding = pgvector.NewVector([]float32{3, 4})
	if err := db.Create(&v2).Error; err != nil {
		t.Fatalf("database rejected a second embedding version: %v", err)
	}
	var persistedV1, persistedV2 models.PostEmbedding
	if err := db.Where("post_id = ? AND version = ?", article.ID, "v1").First(&persistedV1).Error; err != nil {
		t.Fatalf("load v1 embedding after v2 insert: %v", err)
	}
	if err := db.Where("post_id = ? AND version = ?", article.ID, "v2").First(&persistedV2).Error; err != nil {
		t.Fatalf("load v2 embedding: %v", err)
	}
	if persistedV1.Model != "test" || persistedV1.Embedding.Slice()[0] != 1 || persistedV2.Model != "test-v2" || persistedV2.Embedding.Slice()[0] != 3 {
		t.Fatalf("coexisting embedding rows changed: v1=%+v v2=%+v", persistedV1, persistedV2)
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

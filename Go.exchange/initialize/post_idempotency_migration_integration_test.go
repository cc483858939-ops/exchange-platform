package initialize

import (
	"errors"
	"os"
	"strings"
	"testing"

	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostClientPublishIdentityMigrationEnforcesPairAndAuthorScopedUniquenessIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previousDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = previousDB })
	if err := RunMigrations(); err != nil {
		t.Fatal(err)
	}

	user := models.User{Username: "publish-migration-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	var postIDs []uint
	t.Cleanup(func() {
		if len(postIDs) > 0 {
			db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
		}
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	clientPublishID := uuid.New()
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	first := models.Post{
		AuthorID: user.ID, Content: "first keyed post", Language: "und", Visibility: "public",
		ClientPublishID: &clientPublishID, ClientPublishFingerprint: &fingerprint,
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, first.ID)

	duplicate := models.Post{
		AuthorID: user.ID, Content: "duplicate keyed post", Language: "und", Visibility: "public",
		ClientPublishID: &clientPublishID, ClientPublishFingerprint: &fingerprint,
	}
	err = db.Create(&duplicate).Error
	if err == nil {
		t.Fatal("same author and client publish ID was accepted twice")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "uidx_posts_author_client_publish_id" {
		t.Fatalf("duplicate error=%v", err)
	}

	legacyOne := models.Post{AuthorID: user.ID, Content: "legacy one", Language: "und", Visibility: "public"}
	legacyTwo := models.Post{AuthorID: user.ID, Content: "legacy two", Language: "und", Visibility: "public"}
	if err := db.Create(&legacyOne).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&legacyTwo).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, legacyOne.ID, legacyTwo.ID)

	invalidPairID := uuid.New()
	invalidPair := models.Post{
		AuthorID: user.ID, Content: "invalid pair", Language: "und", Visibility: "public",
		ClientPublishID: &invalidPairID,
	}
	err = db.Create(&invalidPair).Error
	if err == nil {
		t.Fatal("client publish ID without fingerprint was accepted")
	}
	pgErr = nil
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "chk_posts_client_publish_identity" {
		t.Fatalf("invalid pair error=%v", err)
	}

	var index struct {
		Definition string `gorm:"column:indexdef"`
	}
	if err := db.Raw(`
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'posts'
  AND indexname = 'uidx_posts_author_client_publish_id'
`).Scan(&index).Error; err != nil {
		t.Fatal(err)
	}
	if index.Definition == "" {
		t.Fatal("client publish unique index is missing")
	}
	if strings.Contains(strings.ToLower(index.Definition), "deleted_at") {
		t.Fatalf("client publish unique index is scoped by deleted_at: %s", index.Definition)
	}
}

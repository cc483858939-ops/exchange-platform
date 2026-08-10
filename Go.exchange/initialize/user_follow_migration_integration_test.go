package initialize

import (
	"os"
	"strings"
	"testing"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestUserFollowMigrationIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.User{}, &models.UserFollow{}); err != nil {
		t.Fatal(err)
	}
	if err := applyUserFollowConstraints(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable(&models.UserFollow{}) {
		t.Fatal("user_follows table does not exist")
	}

	var columns []struct {
		ColumnName string `gorm:"column:column_name"`
		Nullable   string `gorm:"column:is_nullable"`
	}
	if err := db.Raw(`
SELECT column_name, is_nullable
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'user_follows'
  AND column_name IN ('follower_id', 'following_id', 'created_at')
`).Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	nullable := map[string]string{}
	for _, column := range columns {
		nullable[column.ColumnName] = column.Nullable
	}
	for _, columnName := range []string{"follower_id", "following_id", "created_at"} {
		if nullable[columnName] != "NO" {
			t.Fatalf("%s nullable=%q", columnName, nullable[columnName])
		}
	}

	var indexDefs []struct {
		IndexName string `gorm:"column:indexname"`
		IndexDef  string `gorm:"column:indexdef"`
	}
	if err := db.Raw(`
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND tablename = 'user_follows'
  AND indexname IN (
    'uidx_user_follows_pair',
    'idx_user_follows_follower_created',
    'idx_user_follows_following_created'
  )
`).Scan(&indexDefs).Error; err != nil {
		t.Fatal(err)
	}
	indexByName := map[string]string{}
	for _, index := range indexDefs {
		indexByName[index.IndexName] = strings.ToLower(strings.ReplaceAll(index.IndexDef, " ", ""))
	}
	uniquePair := indexByName["uidx_user_follows_pair"]
	if !strings.Contains(uniquePair, "unique") || !strings.Contains(uniquePair, "(follower_id,following_id)") {
		t.Fatalf("unique pair index definition=%q", uniquePair)
	}
	if !strings.Contains(indexByName["idx_user_follows_follower_created"], "(follower_id,created_atdesc,iddesc") {
		t.Fatalf("follower cursor index definition=%q", indexByName["idx_user_follows_follower_created"])
	}
	if !strings.Contains(indexByName["idx_user_follows_following_created"], "(following_id,created_atdesc,iddesc") {
		t.Fatalf("following cursor index definition=%q", indexByName["idx_user_follows_following_created"])
	}

	var checkDefinition string
	if err := db.Raw(`
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'user_follows'::regclass
  AND conname = 'chk_user_follows_not_self'
`).Scan(&checkDefinition).Error; err != nil {
		t.Fatal(err)
	}
	normalizedCheck := strings.ToLower(checkDefinition)
	if !strings.Contains(normalizedCheck, "follower_id") ||
		!strings.Contains(normalizedCheck, "following_id") ||
		!strings.Contains(normalizedCheck, "<>") {
		t.Fatalf("self-follow check definition=%q", checkDefinition)
	}

	var foreignKeys []struct {
		Name       string `gorm:"column:conname"`
		Definition string `gorm:"column:definition"`
	}
	if err := db.Raw(`
SELECT conname, pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE conrelid = 'user_follows'::regclass
  AND conname IN ('fk_user_follows_follower', 'fk_user_follows_following')
`).Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	foreignKeyByName := map[string]string{}
	for _, foreignKey := range foreignKeys {
		foreignKeyByName[foreignKey.Name] = strings.ToLower(foreignKey.Definition)
	}
	for _, name := range []string{"fk_user_follows_follower", "fk_user_follows_following"} {
		definition := foreignKeyByName[name]
		if !strings.Contains(definition, "foreign key") ||
			!strings.Contains(definition, "references users") ||
			!strings.Contains(definition, "on update cascade") ||
			!strings.Contains(definition, "on delete cascade") {
			t.Fatalf("%s definition=%q", name, definition)
		}
	}

	users := []models.User{
		{Username: "follow-migration-a-" + uuid.NewString(), Password: "test"},
		{Username: "follow-migration-b-" + uuid.NewString(), Password: "test"},
	}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	userIDs := []uint{users[0].ID, users[1].ID}
	t.Cleanup(func() {
		db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	})

	first := models.UserFollow{FollowerID: users[0].ID, FollowingID: users[1].ID}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := models.UserFollow{FollowerID: users[0].ID, FollowingID: users[1].ID}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("database accepted duplicate follow pair")
	}
	self := models.UserFollow{FollowerID: users[0].ID, FollowingID: users[0].ID}
	if err := db.Create(&self).Error; err == nil {
		t.Fatal("database accepted self-follow")
	}
	missingFollower := models.UserFollow{FollowerID: 999999999, FollowingID: users[1].ID}
	if err := db.Create(&missingFollower).Error; err == nil {
		t.Fatal("database accepted nonexistent follower")
	}
	missingFollowing := models.UserFollow{FollowerID: users[0].ID, FollowingID: 999999998}
	if err := db.Create(&missingFollowing).Error; err == nil {
		t.Fatal("database accepted nonexistent following")
	}
}

package avatarbackfill

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/devdata"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRegularUserCASAndDevDataAtomicUpdateIntegration(t *testing.T) {
	db := openAvatarBackfillIntegrationDB(t)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	user := models.User{Username: "avatar_backfill_it_" + tag, Password: "test-password", AvatarURL: "/api/files/profile-avatars/" + tag + "/old.jpg"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	registryKey := "avatar-backfill-it-" + tag
	account := models.DevDataMirrorAccount{
		RegistryKey:       registryKey,
		Platform:          "x",
		SourceUserID:      tag,
		SourceHandle:      registryKey,
		LocalUserID:       user.ID,
		Category:          "integration",
		Enabled:           true,
		AvatarObjectKey:   "profile-avatars/devdata/" + strings.ToLower(registryKey) + "/" + strings.Repeat("a", 64) + ".jpg",
		AvatarContentHash: strings.Repeat("a", 64),
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("id = ?", account.ID).Delete(&models.DevDataMirrorAccount{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})

	nextUserURL := "/api/files/profile-avatars/users/v1/" + strconv.FormatUint(uint64(user.ID), 10) + "/" + strings.Repeat("b", 64) + ".jpg"
	updated, err := updateRegularUserCAS(context.Background(), db, user.ID, user.AvatarURL, nextUserURL)
	if err != nil || !updated {
		t.Fatalf("regular CAS updated=%v err=%v", updated, err)
	}
	updated, err = updateRegularUserCAS(context.Background(), db, user.ID, user.AvatarURL, "/api/files/profile-avatars/users/v1/other.jpg")
	if err != nil || updated {
		t.Fatalf("stale regular CAS updated=%v err=%v", updated, err)
	}

	legacyKey := account.AvatarObjectKey
	legacyHash := account.AvatarContentHash
	if err := db.Model(&user).Update("avatar_url", "/api/files/"+legacyKey).Error; err != nil {
		t.Fatal(err)
	}
	newDevDataKey, err := devdata.BuildAvatarObjectKeyV1(registryKey, strings.Repeat("c", 64), ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	captured := capturedDevDataAccount{ID: account.ID, RegistryKey: registryKey, LocalUserID: user.ID, AvatarObjectKey: legacyKey, AvatarContentHash: legacyHash, UserAvatarURL: "/api/files/" + legacyKey}
	if err := updateDevDataAtomically(context.Background(), db, captured, newDevDataKey, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("atomic DevData update: %v", err)
	}
	var gotUser models.User
	if err := db.First(&gotUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	var gotAccount models.DevDataMirrorAccount
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotUser.AvatarURL != "/api/files/"+newDevDataKey || gotAccount.AvatarObjectKey != newDevDataKey || gotAccount.AvatarContentHash != strings.Repeat("c", 64) {
		t.Fatalf("atomic update user=%#v account=%#v", gotUser, gotAccount)
	}

	if err := db.Model(&gotUser).Update("avatar_url", "changed-concurrently").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&gotAccount).Updates(map[string]any{"avatar_object_key": legacyKey, "avatar_content_hash": legacyHash}).Error; err != nil {
		t.Fatal(err)
	}
	if err := updateDevDataAtomically(context.Background(), db, captured, newDevDataKey, strings.Repeat("c", 64)); !errors.Is(err, ErrConcurrentChange) {
		t.Fatalf("concurrent DevData update err=%v", err)
	}
	if err := db.First(&gotUser, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&gotAccount, account.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotUser.AvatarURL != "changed-concurrently" || gotAccount.AvatarObjectKey != legacyKey || gotAccount.AvatarContentHash != legacyHash {
		t.Fatalf("concurrent update overwrote metadata user=%#v account=%#v", gotUser, gotAccount)
	}
}

func openAvatarBackfillIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run avatar backfill integration tests")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open PostgreSQL test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL test database handle: %v", err)
	}
	previous := global.Db
	global.Db = db
	t.Cleanup(func() {
		global.Db = previous
		_ = sqlDB.Close()
	})
	if err := initialize.RunMigrations(); err != nil {
		t.Fatalf("run avatar backfill migrations: %v", err)
	}
	return db
}

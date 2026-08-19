package models_test

import (
	"os"
	"testing"
	"time"

	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func openUserRecoProfileIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("SKIPPED — POSTGRES_TEST_DSN unavailable")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	originalDB := global.Db
	global.Db = db
	t.Cleanup(func() { global.Db = originalDB })
	if err := initialize.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	return db
}

func newUserRecoProfileIntegrationUser(t *testing.T, db *gorm.DB, label string) models.User {
	t.Helper()
	user := models.User{Username: "p1a-follow-up-vector-" + label + "-" + uuid.NewString(), Password: "test"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfileDirty{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.UserRecoProfile{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	return user
}

func userRecoProfileIntegrationVector(values []float32) *pgvector.Vector {
	vector := pgvector.NewVector(values)
	return &vector
}

func TestUserRecoProfileNullableVectorPostgreSQLContractIntegration(t *testing.T) {
	db := openUserRecoProfileIntegrationDB(t)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		dimensions int
		positive   *pgvector.Vector
		negative   *pgvector.Vector
		wantPos    bool
		wantNeg    bool
	}{
		{name: "NULL / NULL", dimensions: 0},
		{name: "positive-only", dimensions: 2, positive: userRecoProfileIntegrationVector([]float32{1, 0}), wantPos: true},
		{name: "negative-only", dimensions: 2, negative: userRecoProfileIntegrationVector([]float32{0, 1}), wantNeg: true},
		{name: "both", dimensions: 2, positive: userRecoProfileIntegrationVector([]float32{1, 0}), negative: userRecoProfileIntegrationVector([]float32{0, 1}), wantPos: true, wantNeg: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user := newUserRecoProfileIntegrationUser(t, db, test.name)
			row := models.UserRecoProfile{
				UserID: user.ID, ProfileVersion: "integration-profile", ProfileConfigHash: "integration-hash",
				EmbeddingVersion: "integration-embedding", Dimensions: test.dimensions,
				PositiveVector: test.positive, NegativeVector: test.negative,
				ComputedAt: now, NextRebuildAt: now.Add(time.Hour), UpdatedAt: now,
			}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			var loaded models.UserRecoProfile
			if err := db.First(&loaded, "user_id = ?", user.ID).Error; err != nil {
				t.Fatal(err)
			}
			if (loaded.PositiveVector != nil) != test.wantPos || (loaded.NegativeVector != nil) != test.wantNeg {
				t.Fatalf("loaded vectors positive=%v negative=%v", loaded.PositiveVector != nil, loaded.NegativeVector != nil)
			}
			if test.wantPos && len(loaded.PositiveVector.Slice()) != 2 {
				t.Fatalf("positive vector=%v", loaded.PositiveVector.Slice())
			}
			if test.wantNeg && len(loaded.NegativeVector.Slice()) != 2 {
				t.Fatalf("negative vector=%v", loaded.NegativeVector.Slice())
			}
		})
	}
}

func TestUserRecoProfileDimensionConstraintRejectsMismatchIntegration(t *testing.T) {
	db := openUserRecoProfileIntegrationDB(t)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	user := newUserRecoProfileIntegrationUser(t, db, "dimension-mismatch")
	row := models.UserRecoProfile{
		UserID: user.ID, ProfileVersion: "integration-profile", ProfileConfigHash: "integration-hash",
		EmbeddingVersion: "integration-embedding", Dimensions: 3,
		PositiveVector: userRecoProfileIntegrationVector([]float32{1, 0}),
		ComputedAt:     now, NextRebuildAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := db.Create(&row).Error; err == nil {
		t.Fatal("database accepted a positive vector whose dimensions disagree with the profile declaration")
	}
}

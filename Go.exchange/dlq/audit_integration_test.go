package dlq

import (
	"context"
	"os"
	"testing"
	"time"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestKafkaDLQReplayAuditPersistenceIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&models.KafkaDLQReplay{}); err != nil {
		t.Fatal(err)
	}

	replayID := uuid.NewString()
	t.Cleanup(func() { _ = db.Unscoped().Delete(&models.KafkaDLQReplay{}, "id = ?", replayID).Error })
	startedAt := time.Now().UTC()
	completedAt := startedAt.Add(time.Second)
	replay := models.KafkaDLQReplay{
		ID: replayID, DLQTopic: "goexchange.consumer.dlq.v1", DLQPartition: 1, DLQOffset: 25,
		Consumer: "user_behavior_projection", SourceTopic: "goexchange.user.behavior.v1", SourcePartition: 2, SourceOffset: 90,
		EventID: "event-1", ErrorCode: "unsupported_schema", Status: ReplayStatusStarted, StartedAt: startedAt,
	}
	repository := GORMAuditRepository{DB: db}
	if err := repository.Start(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
	if err := repository.Complete(context.Background(), replayID, ReplayStatusSucceeded, completedAt, ""); err != nil {
		t.Fatal(err)
	}

	var persisted models.KafkaDLQReplay
	if err := db.First(&persisted, "id = ?", replayID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.CompletedAt == nil {
		t.Fatalf("persisted audit has no completion timestamp: %+v", persisted)
	}
	delta := persisted.CompletedAt.Sub(completedAt)
	if persisted.Status != ReplayStatusSucceeded || delta <= -time.Microsecond || delta >= time.Microsecond || persisted.Error != "" {
		t.Fatalf("persisted audit=%+v want succeeded with completion timestamp", persisted)
	}
}

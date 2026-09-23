package tasks

import (
	"fmt"
	"strings"
	"testing"

	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func installKafkaRecoveryInsertFailureTrigger(t *testing.T, db *gorm.DB, table, predicate string) func() {
	t.Helper()
	switch table {
	case "outbox_events", "recommendation_daily_metrics":
	default:
		t.Fatalf("unsupported recovery integration trigger table %q", table)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "test_kafka_recovery_insert_failure_" + suffix
	triggerName := "test_kafka_recovery_insert_failure_trigger_" + suffix
	if err := db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected Kafka recovery integration failure'; RETURN NEW; END $$`, functionName)).Error; err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(predicate) == "" {
		t.Fatal("recovery integration trigger predicate is required")
	}
	active := true
	drop := func() {
		if !active {
			return
		}
		active = false
		db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON %s`, triggerName, table))
		db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	}
	t.Cleanup(drop)
	if err := db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION %s()`, triggerName, table, predicate, functionName)).Error; err != nil {
		t.Fatal(err)
	}
	return drop
}

func countRecoveryInboxEvent(t *testing.T, db *gorm.DB, consumerName, eventID string) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&models.ConsumerInbox{}).Where("consumer_name = ? AND event_id = ?", consumerName, eventID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

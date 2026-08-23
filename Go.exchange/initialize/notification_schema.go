package initialize

import (
	"errors"
	"fmt"

	"Go.exchange/models"

	"gorm.io/gorm"
)

func applyOutboxSchema(tx *gorm.DB) error {
	if tx == nil || !tx.Migrator().HasTable(&models.OutboxEvent{}) {
		return errors.New("outbox_events table is missing")
	}
	statements := []string{
		"ALTER TABLE outbox_events ALTER COLUMN id SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN topic SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN partition_key SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN event_type SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN schema_version SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN aggregate_type SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN aggregate_id SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN message SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN occurred_at SET NOT NULL",
		"ALTER TABLE outbox_events ALTER COLUMN created_at SET NOT NULL",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_id",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_id CHECK (id IS NOT NULL)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_topic",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_topic CHECK (char_length(btrim(topic)) > 0)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_partition_key",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_partition_key CHECK (char_length(btrim(partition_key)) > 0)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_event_type",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_event_type CHECK (char_length(btrim(event_type)) > 0)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_schema_version",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_schema_version CHECK (schema_version >= 1)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_aggregate_type",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_aggregate_type CHECK (char_length(btrim(aggregate_type)) > 0)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_aggregate_id",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_aggregate_id CHECK (char_length(btrim(aggregate_id)) > 0)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_message_object",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_message_object CHECK (jsonb_typeof(message) = 'object')",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_envelope_id",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_envelope_id CHECK (message->>'id' = id::text)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_envelope_type",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_envelope_type CHECK (message->>'type' = event_type)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_envelope_schema_version",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_envelope_schema_version CHECK ((message->>'schema_version')::integer = schema_version)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_envelope_aggregate_type",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_envelope_aggregate_type CHECK (message->>'aggregate_type' = aggregate_type)",
		"ALTER TABLE outbox_events DROP CONSTRAINT IF EXISTS chk_outbox_events_envelope_aggregate_id",
		"ALTER TABLE outbox_events ADD CONSTRAINT chk_outbox_events_envelope_aggregate_id CHECK (message->>'aggregate_id' = aggregate_id)",
		"CREATE INDEX IF NOT EXISTS idx_outbox_events_created_id ON outbox_events (created_at ASC, id ASC)",
		"CREATE INDEX IF NOT EXISTS idx_outbox_events_type_created ON outbox_events (event_type, created_at DESC)",
		"DROP INDEX IF EXISTS idx_outbox_events_pending",
		`CREATE OR REPLACE FUNCTION reject_outbox_event_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'outbox_events is append-only';
END;
$$`,
		"DROP TRIGGER IF EXISTS trg_outbox_events_append_only ON outbox_events",
		"CREATE TRIGGER trg_outbox_events_append_only BEFORE UPDATE ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_outbox_event_update()",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply outbox schema: %w", err)
		}
	}
	return nil
}

func applyNotificationSchema(tx *gorm.DB) error {
	if tx == nil || !tx.Migrator().HasTable(&models.Notification{}) {
		return errors.New("notifications table is missing")
	}
	statements := []string{
		"ALTER TABLE notifications ALTER COLUMN recipient_id SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN actor_id SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN notification_type SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN dedupe_key SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN source_version SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN activity_at SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN created_at SET NOT NULL",
		"ALTER TABLE notifications ALTER COLUMN updated_at SET NOT NULL",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_type",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_type CHECK (notification_type IN ('post_liked', 'post_replied', 'user_followed'))",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_recipient_actor",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_recipient_actor CHECK (recipient_id <> actor_id)",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_dedupe_key",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_dedupe_key CHECK (char_length(btrim(dedupe_key)) > 0)",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_source_version",
		"ALTER TABLE notifications ADD CONSTRAINT chk_notifications_source_version CHECK (source_version >= 0)",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notifications_shape",
		`ALTER TABLE notifications ADD CONSTRAINT chk_notifications_shape CHECK (
  (notification_type = 'post_liked' AND article_id IS NOT NULL AND comment_id IS NULL AND source_version > 0) OR
  (notification_type = 'post_replied' AND article_id IS NOT NULL AND comment_id IS NOT NULL AND source_version = 0) OR
  (notification_type = 'user_followed' AND article_id IS NULL AND comment_id IS NULL AND source_version > 0)
)`,
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS fk_notifications_recipient",
		"ALTER TABLE notifications ADD CONSTRAINT fk_notifications_recipient FOREIGN KEY (recipient_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS fk_notifications_actor",
		"ALTER TABLE notifications ADD CONSTRAINT fk_notifications_actor FOREIGN KEY (actor_id) REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS fk_notifications_article",
		"ALTER TABLE notifications ADD CONSTRAINT fk_notifications_article FOREIGN KEY (article_id) REFERENCES articles(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"ALTER TABLE notifications DROP CONSTRAINT IF EXISTS fk_notifications_comment",
		"ALTER TABLE notifications ADD CONSTRAINT fk_notifications_comment FOREIGN KEY (comment_id) REFERENCES comments(id) ON UPDATE CASCADE ON DELETE CASCADE",
		"CREATE UNIQUE INDEX IF NOT EXISTS uidx_notifications_dedupe_key ON notifications (dedupe_key)",
		"CREATE INDEX IF NOT EXISTS idx_notifications_recipient_activity ON notifications (recipient_id, activity_at DESC, id DESC)",
		"CREATE INDEX IF NOT EXISTS idx_notifications_unread_recipient_activity ON notifications (recipient_id, activity_at DESC, id DESC) WHERE read_at IS NULL",
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return fmt.Errorf("apply notification schema: %w", err)
		}
	}
	return nil
}

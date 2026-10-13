package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"
)

const kafkaFailureCodeDatabaseForeignKey = "database_foreign_key"

type kafkaForeignKeyParentLookup func(context.Context, *gorm.DB, string, uint) (bool, error)

type kafkaMessageIdentity struct {
	Topic     string
	Partition int
	Offset    int64
}

func kafkaMessageIdentityOf(message kafka.Message) kafkaMessageIdentity {
	return kafkaMessageIdentity{Topic: message.Topic, Partition: message.Partition, Offset: message.Offset}
}

func kafkaPostgresForeignKeyConstraint(err error) (string, bool) {
	var postgresErr *pgconn.PgError
	if !errors.As(err, &postgresErr) || postgresErr == nil || postgresErr.Code != "23503" {
		return "", false
	}
	constraint := strings.TrimSpace(postgresErr.ConstraintName)
	return constraint, constraint != ""
}

func kafkaPostgresForeignKeyIsAllowed(err error, allowlist ...string) bool {
	constraint, ok := kafkaPostgresForeignKeyConstraint(err)
	if !ok {
		return false
	}
	for _, allowed := range allowlist {
		if constraint == allowed {
			return true
		}
	}
	return false
}

func kafkaForeignKeyParent(constraint string, userID, postID uint) (string, uint, bool) {
	switch constraint {
	case "fk_post_behaviors_user", "fk_post_reaction_user", "fk_user_reco_profile_dirty_user", "fk_notifications_recipient", "fk_notifications_actor":
		return "users", userID, userID != 0
	case "fk_post_behaviors_post", "fk_post_reaction_post", "fk_notifications_post":
		return "posts", postID, postID != 0
	default:
		return "", 0, false
	}
}

// lookupKafkaForeignKeyParent deliberately queries the physical table without a
// model scope so soft deletion does not look like a missing foreign-key row.
func lookupKafkaForeignKeyParent(ctx context.Context, db *gorm.DB, table string, id uint) (bool, error) {
	if ctx == nil {
		return false, errors.New("foreign-key parent lookup context is nil")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if db == nil {
		return false, errors.New("database is not initialized")
	}
	if (table != "users" && table != "posts") || id == 0 {
		return false, errors.New("foreign-key parent lookup target is invalid")
	}
	var count int64
	if err := db.WithContext(ctx).Table(table).Where("id = ?", id).Count(&count).Error; err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return count > 0, nil
}

// applyKafkaBatchWithForeignKeyIsolation keeps the successful batch fast path
// to one Apply call. A whitelisted PostgreSQL FK failure is recursively split
// in source order; for a batch of at most 500 records this performs at most 999
// bounded Apply calls and never starts concurrent transactions.
func applyKafkaBatchWithForeignKeyIsolation[T any](
	ctx context.Context,
	records []T,
	apply func(context.Context, []T) error,
	isAllowedForeignKey func(error) bool,
	handleSingleton func(context.Context, T, error) error,
) error {
	if ctx == nil {
		return errors.New("foreign-key recovery context is nil")
	}
	if apply == nil {
		return errors.New("foreign-key recovery apply function is nil")
	}
	if len(records) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := apply(ctx, records)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isAllowedForeignKey == nil || !isAllowedForeignKey(err) {
		return err
	}
	if len(records) == 1 {
		if handleSingleton == nil {
			return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, err)
		}
		return handleSingleton(ctx, records[0], err)
	}
	middle := len(records) / 2
	if err := applyKafkaBatchWithForeignKeyIsolation(ctx, records[:middle], apply, isAllowedForeignKey, handleSingleton); err != nil {
		return err
	}
	return applyKafkaBatchWithForeignKeyIsolation(ctx, records[middle:], apply, isAllowedForeignKey, handleSingleton)
}

// publishConfirmedMissingParentKafkaForeignKeyDLQ only routes a message after
// its failed transaction has returned, the exact referenced parent is absent,
// and the source's producer lifecycle guarantees that parent existed before
// the event could be emitted. A lookup error or a present parent stays retryable.
func publishConfirmedMissingParentKafkaForeignKeyDLQ(
	ctx context.Context,
	db *gorm.DB,
	lookup kafkaForeignKeyParentLookup,
	publisher rawKafkaMessagePublisher,
	dlqTopic, consumer string,
	source kafka.Message,
	fkErr error,
	userID, postID uint,
) error {
	if ctx == nil {
		return errors.New("foreign-key DLQ context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	constraint, ok := kafkaPostgresForeignKeyConstraint(fkErr)
	if !ok {
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, fkErr)
	}
	table, id, ok := kafkaForeignKeyParent(constraint, userID, postID)
	if !ok {
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, fkErr)
	}
	if lookup == nil {
		lookup = lookupKafkaForeignKeyParent
	}
	exists, err := lookup(ctx, db, table, id)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return retryableKafkaError(kafkaFailureCodeDatabaseUnavailable, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if exists {
		return retryableKafkaError(kafkaFailureCodeDatabaseTransaction, fkErr)
	}
	permanentErr := permanentKafkaError(kafkaFailureCodeDatabaseForeignKey,
		fmt.Errorf("constraint %s references missing %s id %d: %w", constraint, table, id, fkErr))
	return publishConsumerDLQ(ctx, publisher, dlqTopic, consumer, source, permanentErr, 1)
}

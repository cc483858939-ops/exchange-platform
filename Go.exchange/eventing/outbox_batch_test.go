package eventing

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type outboxBatchStatement struct {
	query string
	vars  []interface{}
}

func outboxBatchDryRunDB(t *testing.T, failAt int) (*gorm.DB, *[]outboxBatchStatement) {
	t.Helper()
	db, err := gorm.Open(postgres.Open("host=127.0.0.1 user=unused dbname=unused"), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	statements := []outboxBatchStatement{}
	err = db.Callback().Create().After("gorm:create").Register("test:capture_outbox_batch", func(tx *gorm.DB) {
		statements = append(statements, outboxBatchStatement{tx.Statement.SQL.String(), append([]interface{}(nil), tx.Statement.Vars...)})
		if len(statements) == failAt {
			tx.AddError(errors.New("injected chunk failure"))
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, &statements
}

func outboxBatchEvents(t *testing.T, count int) []models.OutboxEvent {
	t.Helper()
	events := make([]models.OutboxEvent, count)
	for i := range events {
		envelope, err := NewPostReactionAppliedEnvelope(uuid.NewString(), PostReactionAppliedPayload{
			ActorID: uint(i + 1), PostID: 42, PostAuthorID: 99, Liked: i%2 == 0,
			ReactionVersion: int64(i + 1), StateChangedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
		events[i], err = NewOutboxEvent(config.KafkaConfig{ActivityEventsTopic: "activity-test"}, envelope)
		if err != nil {
			t.Fatal(err)
		}
	}
	return events
}

func TestAddOutboxEventsBatchesParametersAndPreservesEveryRow(t *testing.T) {
	for _, count := range []int{0, 1, 500, 501, 1001} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			db, statements := outboxBatchDryRunDB(t, 0)
			events := outboxBatchEvents(t, count)
			if err := AddOutboxEvents(db, events); err != nil {
				t.Fatal(err)
			}
			if len(*statements) != (count+499)/500 {
				t.Fatalf("INSERTs=%d events=%d", len(*statements), count)
			}
			index := 0
			for _, statement := range *statements {
				if !strings.HasPrefix(statement.query, `INSERT INTO "outbox_events"`) || strings.Contains(statement.query, "ON CONFLICT") {
					t.Fatal("changed insert or deduplication contract")
				}
				if len(statement.vars)%10 != 0 || len(statement.vars) > 5000 {
					t.Fatal("parameter batch is unbounded")
				}
				for offset := 0; offset < len(statement.vars); offset += 10 {
					row := events[index]
					want := []interface{}{row.ID, row.Topic, row.PartitionKey, row.EventType, row.SchemaVersion, row.AggregateType, row.AggregateID, row.Message, row.OccurredAt, row.CreatedAt}
					for column := range want {
						if statement.vars[offset+column] != want[column] {
							t.Fatalf("row %d column %d changed", index, column)
						}
					}
					index++
				}
			}
			if index != count {
				t.Fatal("event dropped")
			}
		})
	}
}

func TestAddOutboxEventsCapsJSONBytesAndAllowsSingleLargeMessage(t *testing.T) {
	for _, sizes := range [][]int{{600 << 10, 600 << 10, 600 << 10}, {1 << 20, 1 << 20}, {(1 << 20) + 1, 1000}} {
		db, statements := outboxBatchDryRunDB(t, 0)
		events := outboxBatchEvents(t, len(sizes))
		for i, size := range sizes {
			// JSON trailing whitespace preserves the full valid envelope and lets
			// this generic batching helper exercise larger event families.
			events[i].Message += strings.Repeat(" ", size-len(events[i].Message))
		}
		if err := AddOutboxEvents(db, events); err != nil {
			t.Fatal(err)
		}
		if len(*statements) != len(sizes) {
			t.Fatal("oversized JSON batch was not split")
		}
		for _, statement := range *statements {
			if len(statement.vars) != 10 {
				t.Fatal("large message not written alone")
			}
		}
	}
}

func TestAddOutboxEventsStopsAfterFailedChunkAndRejectsNilTransaction(t *testing.T) {
	if err := AddOutboxEvents(nil, nil); err == nil {
		t.Fatal("nil transaction accepted")
	}
	db, statements := outboxBatchDryRunDB(t, 2)
	if err := AddOutboxEvents(db, outboxBatchEvents(t, 1001)); err == nil {
		t.Fatal("chunk failure swallowed")
	}
	if len(*statements) != 2 {
		t.Fatal("later chunk attempted after failure")
	}
}

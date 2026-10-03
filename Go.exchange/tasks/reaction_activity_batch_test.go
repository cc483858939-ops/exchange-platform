package tasks

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A transaction-aware database/sql fixture runs the actual projection and
// GORM inserts. External PostgreSQL semantics are verified separately below.
type reactionBatchState struct {
	inbox, reactions, dirty int
	activities              []eventing.Envelope
}
type reactionBatchDB struct {
	committed, pending                                     reactionBatchState
	active                                                 bool
	begins, commits, rollbacks, authorReads, outboxInserts int
	failChunk                                              int
	failDirty, invalidLast                                 bool
}
type reactionBatchConnector struct{ fixture *reactionBatchDB }
type reactionBatchDriver struct{}
type reactionBatchTx struct{ fixture *reactionBatchDB }

func (c reactionBatchConnector) Connect(context.Context) (driver.Conn, error) { return c, nil }
func (reactionBatchConnector) Driver() driver.Driver                          { return reactionBatchDriver{} }
func (reactionBatchDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected open")
}
func (reactionBatchConnector) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (reactionBatchConnector) Close() error { return nil }
func (c reactionBatchConnector) Begin() (driver.Tx, error) {
	if c.fixture.active {
		return nil, errors.New("unexpected nested transaction")
	}
	c.fixture.active = true
	c.fixture.begins++
	c.fixture.pending = c.fixture.committed
	return reactionBatchTx{c.fixture}, nil
}
func (tx reactionBatchTx) Commit() error {
	tx.fixture.committed = tx.fixture.pending
	tx.fixture.active = false
	tx.fixture.commits++
	return nil
}
func (tx reactionBatchTx) Rollback() error {
	tx.fixture.pending = reactionBatchState{}
	tx.fixture.active = false
	tx.fixture.rollbacks++
	return nil
}

type reactionBatchRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *reactionBatchRows) Columns() []string { return r.columns }
func (r *reactionBatchRows) Close() error      { return nil }
func (r *reactionBatchRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func (c reactionBatchConnector) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	f := c.fixture
	if !f.active {
		return nil, errors.New("query escaped projection transaction")
	}
	switch {
	case strings.Contains(query, "INSERT INTO consumer_inboxes"):
		rows := &reactionBatchRows{columns: []string{"event_id"}}
		for i := 0; i < len(args); i += 3 {
			rows.values = append(rows.values, []driver.Value{args[i+1].Value})
			f.pending.inbox++
		}
		return rows, nil
	case strings.Contains(query, "INSERT INTO post_reaction"):
		if !strings.Contains(query, "WHERE post_reaction.reaction_version < EXCLUDED.reaction_version") {
			return nil, errors.New("version gate removed")
		}
		rows := &reactionBatchRows{columns: []string{"user_id", "post_id", "reaction_version", "liked", "state_changed_at"}}
		for i := 0; i < len(args); i += 7 {
			at := args[i+6].Value
			if f.invalidLast && i+7 == len(args) {
				at = time.Time{}
			}
			rows.values = append(rows.values, []driver.Value{args[i].Value, args[i+1].Value, args[i+4].Value, args[i+3].Value, at})
			f.pending.reactions++
		}
		return rows, nil
	case strings.Contains(query, `FROM "posts"`):
		f.authorReads++
		return &reactionBatchRows{[]string{"id", "author_id"}, [][]driver.Value{{int64(42), int64(99)}}}, nil
	default:
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
}

func (c reactionBatchConnector) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	f := c.fixture
	if !f.active {
		return nil, errors.New("write escaped projection transaction")
	}
	switch {
	case strings.HasPrefix(query, `INSERT INTO "outbox_events"`):
		f.outboxInserts++
		if f.outboxInserts == f.failChunk {
			return nil, errors.New("injected later Outbox chunk failure")
		}
		if len(args)%10 != 0 || len(args) > 5000 {
			return nil, errors.New("invalid Outbox parameter batch")
		}
		for i := 0; i < len(args); i += 10 {
			var envelope eventing.Envelope
			if err := json.Unmarshal([]byte(args[i+7].Value.(string)), &envelope); err != nil {
				return nil, err
			}
			if envelope.ID != args[i].Value || args[i+1].Value != "activity-test" || args[i+2].Value != envelope.AggregateID || args[i+3].Value != eventing.EventTypePostReactionApplied {
				return nil, errors.New("changed Outbox identity/routing contract")
			}
			f.pending.activities = append(f.pending.activities, envelope)
		}
		return driver.RowsAffected(len(args) / 10), nil
	case strings.HasPrefix(query, `INSERT INTO "user_reco_profile_dirty"`):
		if f.failDirty {
			return nil, errors.New("injected post-Outbox profile failure")
		}
		f.pending.dirty++
		return driver.RowsAffected(1), nil
	default:
		return nil, fmt.Errorf("unexpected exec: %s", query)
	}
}

func reactionActivityFixture(t *testing.T) (*gorm.DB, *reactionBatchDB) {
	t.Helper()
	f := &reactionBatchDB{}
	connection := sql.OpenDB(reactionBatchConnector{f})
	t.Cleanup(func() { _ = connection.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: connection}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return db, f
}

func reactionActivityRecords(count int) []userBehaviorEventRecord {
	records := make([]userBehaviorEventRecord, count)
	for i := range records {
		eventType := eventing.EventTypePostLiked
		if i%2 == 1 {
			eventType = eventing.EventTypePostUnliked
		}
		records[i] = userBehaviorEventRecord{
			Envelope: eventing.Envelope{ID: uuid.NewString(), Type: eventType, OccurredAt: time.Date(2026, 10, 3, 0, 0, i%60, i, time.UTC)},
			Payload:  eventing.UserBehaviorPayload{UserID: uint(i + 7), PostID: 42, LikeVersion: int64(i + 1)},
		}
	}
	return records
}

func TestAppliedReactionActivitiesUseOneInsertFor500AndKeepEventContract(t *testing.T) {
	db, f := reactionActivityFixture(t)
	records := reactionActivityRecords(500)
	err := applyUserBehaviorRecords(context.Background(), db, "reaction-batch", config.KafkaConfig{ActivityEventsTopic: "activity-test"}, records)
	if err != nil {
		t.Fatal(err)
	}
	if f.begins != 1 || f.commits != 1 || f.rollbacks != 0 || f.authorReads != 1 || f.outboxInserts != 1 {
		t.Fatalf("transaction/query counts: %+v", f)
	}
	if f.committed.inbox != 500 || f.committed.reactions != 500 || len(f.committed.activities) != 500 || f.committed.dirty != 1 {
		t.Fatal("projection writes were omitted")
	}
	seen := map[string]bool{}
	for i, envelope := range f.committed.activities {
		if _, err := uuid.Parse(envelope.ID); err != nil || seen[envelope.ID] {
			t.Fatal("event UUID missing or duplicated")
		}
		seen[envelope.ID] = true
		var payload eventing.PostReactionAppliedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		want := records[i]
		if payload.ActorID != want.Payload.UserID || payload.PostID != 42 || payload.PostAuthorID != 99 || payload.ReactionVersion != want.Payload.LikeVersion || payload.Liked != (want.Envelope.Type == eventing.EventTypePostLiked) || !payload.StateChangedAt.Equal(want.Envelope.OccurredAt) || envelope.SchemaVersion != 1 || envelope.AggregateType != "post_reaction" {
			t.Fatalf("event contract changed at row %d", i)
		}
	}
}

func TestReactionOutboxBatchFailureRollsBackEntireProjectionAndCanRetry(t *testing.T) {
	for _, scenario := range []string{"second chunk", "after Outbox", "last event invalid"} {
		t.Run(scenario, func(t *testing.T) {
			db, f := reactionActivityFixture(t)
			count := 501 // Cross the helper's chunk boundary, even for future larger callers.
			if scenario == "last event invalid" {
				count = 2
				f.invalidLast = true
			}
			if scenario == "second chunk" {
				f.failChunk = 2
			}
			if scenario == "after Outbox" {
				f.failDirty = true
			}
			records := reactionActivityRecords(count)
			err := applyUserBehaviorRecords(context.Background(), db, "reaction-batch", config.KafkaConfig{ActivityEventsTopic: "activity-test"}, records)
			if kafkaFailureClassOf(err) != kafkaFailureRetryable || kafkaFailureCode(err) != kafkaFailureCodeDatabaseTransaction {
				t.Fatalf("failure lost retryable contract: %v", err)
			}
			if f.commits != 0 || f.rollbacks != 1 || f.committed.inbox != 0 || f.committed.reactions != 0 || len(f.committed.activities) != 0 || f.committed.dirty != 0 {
				t.Fatal("partial projection committed")
			}
			if scenario == "last event invalid" && f.outboxInserts != 0 {
				t.Fatal("Outbox written before complete batch validation")
			}
			f.failChunk = 0
			f.failDirty = false
			f.invalidLast = false
			f.outboxInserts = 0
			if err := applyUserBehaviorRecords(context.Background(), db, "reaction-batch", config.KafkaConfig{ActivityEventsTopic: "activity-test"}, records); err != nil {
				t.Fatalf("redelivery could not recover: %v", err)
			}
			if f.committed.inbox != count || f.committed.reactions != count || len(f.committed.activities) != count {
				t.Fatal("redelivery lost rows")
			}
		})
	}
}

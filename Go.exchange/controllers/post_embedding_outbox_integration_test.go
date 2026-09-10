package controllers

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	testPostEmbeddingTopic = "goexchange.post.embedding.events.v1"
	testActivityTopic      = "goexchange.activity.events.v1"
)

type postEmbeddingOutboxFixture struct {
	db    *gorm.DB
	users []models.User
	posts []models.Post
}

func openPostEmbeddingOutboxIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.Post{},
		&models.PostMedia{},
		&models.PostBehavior{},
		&models.UserRecoProfileDirty{},
		&models.OutboxEvent{},
	); err != nil {
		t.Fatal(err)
	}

	originalDB, originalConfig := global.Db, config.AppConfig
	global.Db = db
	config.AppConfig = &config.Config{
		Embedding: config.EmbeddingConfig{Enabled: true, Version: "test-version"},
		Kafka: config.KafkaConfig{
			PostEmbeddingTopic:  testPostEmbeddingTopic,
			ActivityEventsTopic: testActivityTopic,
		},
	}
	t.Cleanup(func() {
		global.Db = originalDB
		config.AppConfig = originalConfig
	})
	return db
}

func newPostEmbeddingOutboxFixture(t *testing.T, db *gorm.DB) *postEmbeddingOutboxFixture {
	t.Helper()
	fixture := &postEmbeddingOutboxFixture{db: db}
	t.Cleanup(func() {
		fixture.cleanup()
	})
	for _, prefix := range []string{"embedding-author", "embedding-commenter"} {
		user := models.User{Username: prefix + "-" + uuid.NewString(), Password: "test"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		fixture.users = append(fixture.users, user)
	}
	return fixture
}

func (fixture *postEmbeddingOutboxFixture) cleanup() {
	if fixture.db == nil {
		return
	}
	if len(fixture.posts) > 0 {
		aggregateIDs := make([]string, 0, len(fixture.posts))
		postIDs := make([]uint, 0, len(fixture.posts))
		for _, post := range fixture.posts {
			aggregateIDs = append(aggregateIDs, strconv.FormatUint(uint64(post.ID), 10))
			postIDs = append(postIDs, post.ID)
		}
		fixture.db.Unscoped().Where("aggregate_id IN ?", aggregateIDs).Delete(&models.OutboxEvent{})
		fixture.db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostMedia{})
		fixture.db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
		// Delete children before parents so the self-referencing post FK remains valid.
		sort.Slice(postIDs, func(i, j int) bool { return postIDs[i] > postIDs[j] })
		for _, postID := range postIDs {
			fixture.db.Unscoped().Where("id = ?", postID).Delete(&models.Post{})
		}
	}
	if len(fixture.users) > 0 {
		userIDs := make([]uint, 0, len(fixture.users))
		for _, user := range fixture.users {
			userIDs = append(userIDs, user.ID)
		}
		fixture.db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserRecoProfileDirty{})
		fixture.db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	}
}

func installRejectingOutboxInsertTrigger(t *testing.T, db *gorm.DB) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "reject_test_embedding_outbox_" + suffix
	triggerName := "trg_test_embedding_outbox_" + suffix
	t.Cleanup(func() {
		_ = db.Exec("DROP TRIGGER IF EXISTS " + triggerName + " ON outbox_events").Error
		_ = db.Exec("DROP FUNCTION IF EXISTS " + functionName + "()").Error
	})
	functionSQL := fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'test embedding outbox insert failure';
END;
$$`, functionName)
	if err := db.Exec(functionSQL).Error; err != nil {
		t.Fatal(err)
	}
	triggerSQL := fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName, functionName,
	)
	if err := db.Exec(triggerSQL).Error; err != nil {
		t.Fatal(err)
	}
}

func loadEmbeddingOutboxRows(t *testing.T, db *gorm.DB, postID uint) []models.OutboxEvent {
	t.Helper()
	var rows []models.OutboxEvent
	if err := db.Where("aggregate_id = ?", strconv.FormatUint(uint64(postID), 10)).Order("event_type ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPostEmbeddingOutboxPersistsCanonicalRequestIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var post models.Post
	if err := persistPostGraph(&post, fixture.users[0].ID, "embedding root", createPostRequest{Content: "embedding root"}, nil, now); err != nil {
		t.Fatal(err)
	}
	fixture.posts = append(fixture.posts, post)

	rows := loadEmbeddingOutboxRows(t, db, post.ID)
	if len(rows) != 1 {
		t.Fatalf("outbox rows=%d want=1: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.EventType != eventing.EventTypePostEmbeddingRequested ||
		row.Topic != testPostEmbeddingTopic ||
		row.PartitionKey != strconv.FormatUint(uint64(post.ID), 10) ||
		row.SchemaVersion != 1 || row.AggregateType != "post" ||
		row.AggregateID != strconv.FormatUint(uint64(post.ID), 10) {
		t.Fatalf("outbox row=%#v", row)
	}
	event, err := eventing.DecodeEnvelope([]byte(row.Message))
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != row.ID || event.Type != eventing.EventTypePostEmbeddingRequested ||
		event.SchemaVersion != 1 || event.AggregateType != "post" ||
		event.AggregateID != strconv.FormatUint(uint64(post.ID), 10) ||
		!event.OccurredAt.Equal(now) {
		t.Fatalf("decoded envelope=%#v row=%#v", event, row)
	}
	var payload eventing.PostEmbeddingRequestedPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.PostID != post.ID {
		t.Fatalf("payload=%#v want post id=%d", payload, post.ID)
	}
}

func TestPostEmbeddingOutboxInsertFailureRollsBackPostIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	installRejectingOutboxInsertTrigger(t, db)

	var post models.Post
	err := persistPostGraph(&post, fixture.users[0].ID, "embedding rollback", createPostRequest{Content: "embedding rollback"}, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("persist unexpectedly succeeded with failing outbox insert")
	}
	if post.ID == 0 {
		t.Fatal("post ID was not assigned before outbox failure")
	}
	var postCount int64
	if err := db.Unscoped().Model(&models.Post{}).
		Where("id = ? AND author_id = ? AND content = ?", post.ID, fixture.users[0].ID, "embedding rollback").
		Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 0 {
		t.Fatalf("rolled-back post rows=%d want=0", postCount)
	}
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).
		Where("aggregate_id = ? AND event_type = ?", strconv.FormatUint(uint64(post.ID), 10), eventing.EventTypePostEmbeddingRequested).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("rolled-back embedding outbox rows=%d want=0", outboxCount)
	}
}

func TestPostEmbeddingOutboxDisabledDoesNotPersistRequestIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Embedding.Enabled = false

	var post models.Post
	if err := persistPostGraph(&post, fixture.users[0].ID, "embedding disabled", createPostRequest{Content: "embedding disabled"}, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	fixture.posts = append(fixture.posts, post)
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).
		Where("aggregate_id = ? AND event_type = ?", strconv.FormatUint(uint64(post.ID), 10), eventing.EventTypePostEmbeddingRequested).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("disabled embedding outbox rows=%d want=0", outboxCount)
	}
}

func TestPostEmbeddingOutboxRequiresTopicAndRollsBackPostIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	config.AppConfig.Kafka.PostEmbeddingTopic = ""

	var post models.Post
	err := persistPostGraph(&post, fixture.users[0].ID, "embedding topic missing", createPostRequest{Content: "embedding topic missing"}, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("persist unexpectedly succeeded without an embedding topic")
	}
	if post.ID == 0 {
		t.Fatal("post ID was not assigned before topic validation failure")
	}
	var postCount int64
	if err := db.Unscoped().Model(&models.Post{}).Where("id = ?", post.ID).Count(&postCount).Error; err != nil {
		t.Fatal(err)
	}
	if postCount != 0 {
		t.Fatalf("post rows=%d want=0", postCount)
	}
	var outboxCount int64
	if err := db.Model(&models.OutboxEvent{}).
		Where("aggregate_id = ?", strconv.FormatUint(uint64(post.ID), 10)).
		Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("outbox rows=%d want=0", outboxCount)
	}
}

func TestReplyPersistsActivityAndEmbeddingOutboxesTogetherIntegration(t *testing.T) {
	db := openPostEmbeddingOutboxIntegrationDatabase(t)
	fixture := newPostEmbeddingOutboxFixture(t, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	parent := models.Post{
		Model:    gorm.Model{CreatedAt: now, UpdatedAt: now},
		AuthorID: fixture.users[0].ID, Content: "embedding parent", Visibility: "public",
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	fixture.posts = append(fixture.posts, parent)
	parentID := parent.ID
	var reply models.Post
	if err := persistPostGraph(&reply, fixture.users[1].ID, "embedding reply", createPostRequest{
		Content:       "embedding reply",
		ReplyToPostID: &parentID,
	}, nil, now); err != nil {
		t.Fatal(err)
	}
	fixture.posts = append(fixture.posts, reply)

	rows := loadEmbeddingOutboxRows(t, db, reply.ID)
	if len(rows) != 2 {
		t.Fatalf("reply outbox rows=%d want=2: %#v", len(rows), rows)
	}
	seen := make(map[string]models.OutboxEvent, len(rows))
	for _, row := range rows {
		seen[row.EventType] = row
	}
	if _, ok := seen[eventing.EventTypePostEmbeddingRequested]; !ok {
		t.Fatalf("missing embedding outbox: %#v", rows)
	}
	if _, ok := seen[eventing.EventTypeReplyCreated]; !ok {
		t.Fatalf("missing reply activity outbox: %#v", rows)
	}
	if seen[eventing.EventTypePostEmbeddingRequested].Topic != testPostEmbeddingTopic {
		t.Fatalf("embedding row=%#v", seen[eventing.EventTypePostEmbeddingRequested])
	}
	if seen[eventing.EventTypeReplyCreated].Topic != testActivityTopic {
		t.Fatalf("activity row=%#v", seen[eventing.EventTypeReplyCreated])
	}
	for eventType, row := range seen {
		event, err := eventing.DecodeEnvelope([]byte(row.Message))
		if err != nil {
			t.Fatalf("decode %s: %v", eventType, err)
		}
		if event.Type != eventType || event.AggregateType != "post" || event.AggregateID != strconv.FormatUint(uint64(reply.ID), 10) {
			t.Fatalf("event %s=%#v", eventType, event)
		}
	}
	var storedReply models.Post
	if err := db.First(&storedReply, reply.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedReply.ReplyToPostID == nil || *storedReply.ReplyToPostID != parent.ID {
		t.Fatalf("reply=%#v", storedReply)
	}
	var storedParent models.Post
	if err := db.First(&storedParent, parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedParent.ReplyCount != 1 {
		t.Fatalf("parent reply_count=%d want=1", storedParent.ReplyCount)
	}
}

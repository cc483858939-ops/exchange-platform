package devdata

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/embeddingstate"
	"Go.exchange/eventing"
	"Go.exchange/models"
	"Go.exchange/recommendation"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

const devDataEmbeddingTestTopic = "post-embedding-integration"

func addDevDataVersionedEmbeddings(t *testing.T, db *gorm.DB, postID uint, content string) {
	t.Helper()
	now := time.Now().UTC()
	rows := []models.PostEmbedding{
		{PostID: postID, Version: "post_embedding_v1", Model: "devdata-integration", Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(content), CreatedAt: now, UpdatedAt: now},
		{PostID: postID, Version: "post_embedding_v2", Model: "devdata-integration", Dimensions: 2, Embedding: pgvector.NewVector([]float32{0, 1}), ContentHash: embeddings.PostEmbeddingContentHash(content), CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create versioned embeddings for Post %d: %v", postID, err)
	}
}

func assertDevDataEmbeddingVersions(t *testing.T, db *gorm.DB, postID uint, want ...string) {
	t.Helper()
	var rows []models.PostEmbedding
	if err := db.Where("post_id = ?", postID).Order("version ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load embeddings for Post %d: %v", postID, err)
	}
	if len(rows) != len(want) {
		t.Fatalf("Post %d embedding versions=%v want=%v", postID, embeddingVersions(rows), want)
	}
	got := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		got[row.Version] = struct{}{}
	}
	for _, version := range want {
		if _, ok := got[version]; !ok {
			t.Fatalf("Post %d embedding versions=%v missing %q", postID, embeddingVersions(rows), version)
		}
	}
}

func embeddingVersions(rows []models.PostEmbedding) []string {
	versions := make([]string, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, row.Version)
	}
	return versions
}

func assertDevDataEmbeddingOutbox(t *testing.T, db *gorm.DB, postID uint, want int) {
	t.Helper()
	var rows []models.OutboxEvent
	if err := db.Where("event_type = ? AND aggregate_id = ?", eventing.EventTypePostEmbeddingRequested, fmt.Sprint(postID)).Order("created_at ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load Post %d embedding outbox rows: %v", postID, err)
	}
	if len(rows) != want {
		t.Fatalf("Post %d embedding outbox rows=%d want=%d", postID, len(rows), want)
	}
	for _, row := range rows {
		var envelope eventing.Envelope
		if err := json.Unmarshal([]byte(row.Message), &envelope); err != nil {
			t.Fatalf("decode Post %d embedding outbox envelope: %v", postID, err)
		}
		var payload eventing.PostEmbeddingRequestedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Fatalf("decode Post %d embedding payload: %v", postID, err)
		}
		if row.Topic != devDataEmbeddingTestTopic || row.EventType != eventing.EventTypePostEmbeddingRequested || row.AggregateType != "post" || row.AggregateID != fmt.Sprint(postID) || envelope.Type != eventing.EventTypePostEmbeddingRequested || payload.PostID != postID {
			t.Fatalf("invalid Post %d embedding outbox row=%#v envelope=%#v payload=%#v", postID, row, envelope, payload)
		}
		if _, err := uuid.Parse(row.ID); err != nil || envelope.ID != row.ID {
			t.Fatalf("invalid Post %d embedding event ID row=%q envelope=%q err=%v", postID, row.ID, envelope.ID, err)
		}
	}
}

func devDataEmbeddingSyncOptions(enabled bool) SyncOptions {
	return SyncOptions{EmbeddingEnabled: enabled, PostEmbeddingTopic: devDataEmbeddingTestTopic}
}

func registerDevDataEmbeddingCleanup(t *testing.T, db *gorm.DB, postID, userID uint) {
	t.Helper()
	t.Cleanup(func() {
		if err := db.Where("event_type = ? AND aggregate_id = ?", eventing.EventTypePostEmbeddingRequested, fmt.Sprint(postID)).Delete(&models.OutboxEvent{}).Error; err != nil {
			t.Errorf("delete Post %d embedding outbox rows: %v", postID, err)
		}
		if userID != 0 {
			if err := db.Where("user_id = ?", userID).Delete(&models.UserRecoProfileDirty{}).Error; err != nil {
				t.Errorf("delete embedding test dirty profile for user %d: %v", userID, err)
			}
		}
	})
}

func TestDevDataEmbeddingInvalidationAcrossSyncPathsIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	target, shard := incrementalIntegrationTarget(t, data)
	post := data.sourcePost(target.Key, 1701, now.Add(-time.Hour), "embedding lifecycle A")
	initial := data.snapshot(now, post)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now); err != nil {
		t.Fatalf("seed embedding lifecycle snapshot: %v", err)
	}
	postID := findMirrorMapping(t, db, post.SourcePostID).LocalPostID
	viewer := models.User{Username: "it_embedding_viewer_" + data.Tag, Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatalf("create embedding test viewer: %v", err)
	}
	registerDevDataEmbeddingCleanup(t, db, postID, viewer.ID)
	behavior := models.PostBehavior{UserID: viewer.ID, PostID: postID, Action: "view", Count: 1, LastSeenAt: now, Active: true}
	if err := db.Create(&behavior).Error; err != nil {
		t.Fatalf("create recommendation behavior for embedding test: %v", err)
	}
	addDevDataVersionedEmbeddings(t, db, postID, post.Text)

	metadataOnly := post
	metadataOnly.Language = "ja"
	metadataOnly.CreatedAt = post.CreatedAt.Add(time.Minute)
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(time.Minute), metadataOnly), nil, now.Add(time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync metadata-only Post change: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID, "post_embedding_v1", "post_embedding_v2")
	assertDevDataEmbeddingOutbox(t, db, postID, 0)

	post = metadataOnly
	post.Text += " body B"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(2*time.Minute), post), nil, now.Add(2*time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync snapshot body change: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID)
	assertDevDataEmbeddingOutbox(t, db, postID, 1)
	var dirty models.UserRecoProfileDirty
	if err := db.Where("user_id = ?", viewer.ID).First(&dirty).Error; err != nil {
		t.Fatalf("load invalidated recommendation profile: %v", err)
	}
	if dirty.Reason != "post_embedding_changed" || dirty.DirtyVersion != 1 {
		t.Fatalf("dirty profile=%#v", dirty)
	}

	post.Text += " body B2"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(3*time.Minute), post), nil, now.Add(3*time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync body change without existing embeddings: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID)
	assertDevDataEmbeddingOutbox(t, db, postID, 2)

	addDevDataVersionedEmbeddings(t, db, postID, post.Text)
	post.Text += " incremental C"
	incremental := IncrementalBatch{
		FetchedAt: now.Add(4 * time.Minute), Shard: shard,
		Accounts: []SnapshotAccount{baselineAccount(t, initial, target.Key)}, Posts: []SnapshotPost{post},
	}
	if _, err := SyncIncremental(context.Background(), db, data.Registry, incremental, nil, now.Add(4*time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync incremental body change: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID)
	assertDevDataEmbeddingOutbox(t, db, postID, 3)

	addDevDataVersionedEmbeddings(t, db, postID, post.Text)
	post.Text += " targeted D"
	targeted := TargetedRefreshBatch{
		RegistryKey: target.Key, FetchedAt: now.Add(5 * time.Minute),
		Accounts: []SnapshotAccount{baselineAccount(t, initial, target.Key)}, Posts: []SnapshotPost{post},
	}
	if _, err := SyncTargeted(context.Background(), db, data.Registry, targeted, nil, now.Add(5*time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync targeted body change: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID)
	assertDevDataEmbeddingOutbox(t, db, postID, 4)

	addDevDataVersionedEmbeddings(t, db, postID, post.Text)
	post.Text += " embedding disabled E"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(6*time.Minute), post), nil, now.Add(6*time.Minute), SyncOptions{}); err != nil {
		t.Fatalf("sync body change with Embedding disabled: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID)
	assertDevDataEmbeddingOutbox(t, db, postID, 4)
}

func TestDevDataBodyChangeInvalidatesServingEmbeddingWithoutBlockingNonSemanticRecallIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	target, _ := incrementalIntegrationTarget(t, data)
	post := data.sourcePost(target.Key, 1710, now.Add(-time.Hour), "recommendation lifecycle A")
	initial := data.snapshot(now, post)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now); err != nil {
		t.Fatalf("seed recommendation lifecycle snapshot: %v", err)
	}
	postID := findMirrorMapping(t, db, post.SourcePostID).LocalPostID
	var storedPost models.Post
	if err := db.First(&storedPost, postID).Error; err != nil {
		t.Fatalf("load recommendation lifecycle Post: %v", err)
	}
	viewer := models.User{Username: "it_embedding_reco_viewer_" + data.Tag, Password: "test"}
	if err := db.Create(&viewer).Error; err != nil {
		t.Fatalf("create recommendation lifecycle viewer: %v", err)
	}
	registerDevDataEmbeddingCleanup(t, db, postID, viewer.ID)
	if err := db.Create(&models.PostBehavior{
		UserID: viewer.ID, PostID: postID, Action: recommendation.PostBehaviorView,
		Count: 1, LastSeenAt: now, Active: true,
	}).Error; err != nil {
		t.Fatalf("create profile invalidation behavior: %v", err)
	}
	addDevDataVersionedEmbeddings(t, db, postID, storedPost.Content)

	repository, err := recommendation.NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	loadServingQuery := func() recommendation.CandidateQuery {
		t.Helper()
		servingVersion, err := embeddingstate.LoadServingVersion(context.Background(), db)
		if err != nil {
			t.Fatalf("load serving version: %v", err)
		}
		cutoff, _ := config.RecommendationPrimaryWindowBounds(now, config.DefaultRecommendationPrimaryWindowDays)
		positiveVector := []float32{1, 0}
		if servingVersion == "post_embedding_v2" {
			positiveVector = []float32{0, 1}
		}
		return recommendation.CandidateQuery{
			UserID: viewer.ID, ServingVersion: servingVersion, PositiveVector: positiveVector, Now: now,
			Range: recommendation.CandidateRangeRecent, RecentCutoff: cutoff, Limit: 10,
		}
	}
	loadServingCandidates := func() (recommendation.CandidateQuery, []recommendation.Candidate) {
		t.Helper()
		query := loadServingQuery()
		candidates, err := repository.LoadRecentCandidates(context.Background(), query)
		if err != nil {
			t.Fatalf("load %s recent recommendation candidates: %v", query.ServingVersion, err)
		}
		return query, candidates
	}
	recentCandidateIDs := func(candidates []recommendation.Candidate) []uint {
		t.Helper()
		ids := make([]uint, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.PostID)
		}
		return ids
	}
	loadServingSemanticCandidateIDs := func(query recommendation.CandidateQuery) []uint {
		t.Helper()
		candidates, err := repository.LoadSemanticCandidates(context.Background(), query)
		if err != nil {
			t.Fatalf("load %s semantic recommendation candidates: %v", query.ServingVersion, err)
		}
		ids := make([]uint, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.PostID)
		}
		return ids
	}
	assertHydratedEmbedding := func(query recommendation.CandidateQuery, candidates []recommendation.Candidate, want []float32) {
		t.Helper()
		hydrated, err := repository.HydrateCandidates(context.Background(), query, candidates)
		if err != nil {
			t.Fatalf("hydrate %s recent recommendation candidates: %v", query.ServingVersion, err)
		}
		for _, candidate := range hydrated {
			if candidate.Post.ID != postID {
				continue
			}
			if want == nil {
				if candidate.Embedding != nil {
					t.Fatalf("Post %d hydrated stale embedding under %s: %v", postID, query.ServingVersion, candidate.Embedding)
				}
				return
			}
			if len(candidate.Embedding) != len(want) {
				t.Fatalf("Post %d hydrated embedding under %s=%v, want %v", postID, query.ServingVersion, candidate.Embedding, want)
			}
			for index := range want {
				if candidate.Embedding[index] != want[index] {
					t.Fatalf("Post %d hydrated embedding under %s=%v, want %v", postID, query.ServingVersion, candidate.Embedding, want)
				}
			}
			return
		}
		t.Fatalf("Post %d was dropped during %s hydration", postID, query.ServingVersion)
	}
	query, recentCandidates := loadServingCandidates()
	if query.ServingVersion != "post_embedding_v1" {
		t.Fatalf("initial serving version=%q, want post_embedding_v1", query.ServingVersion)
	}
	if !containsUintID(recentCandidateIDs(recentCandidates), postID) {
		t.Fatalf("Post %d with v1 serving embedding is not eligible for ordinary recent recall: %v", postID, recentCandidateIDs(recentCandidates))
	}
	if got := loadServingSemanticCandidateIDs(query); !containsUintID(got, postID) {
		t.Fatalf("Post %d with valid v1 embedding is not eligible for v1 semantic recall: %v", postID, got)
	}
	assertHydratedEmbedding(query, recentCandidates, []float32{1, 0})
	assertDevDataEmbeddingOutbox(t, db, postID, 0)

	post.Text += " body B"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(time.Minute), post), nil, now.Add(time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync recommendation lifecycle body change: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID)
	assertDevDataEmbeddingOutbox(t, db, postID, 1)
	var dirty models.UserRecoProfileDirty
	if err := db.Where("user_id = ?", viewer.ID).First(&dirty).Error; err != nil {
		t.Fatalf("load dirty profile after body change: %v", err)
	}
	if dirty.Reason != "post_embedding_changed" {
		t.Fatalf("dirty profile reason=%q, want post_embedding_changed", dirty.Reason)
	}
	query, recentCandidates = loadServingCandidates()
	if !containsUintID(recentCandidateIDs(recentCandidates), postID) {
		t.Fatalf("body-changed Post %d was removed from ordinary recent recall after embedding invalidation: %v", postID, recentCandidateIDs(recentCandidates))
	}
	if got := loadServingSemanticCandidateIDs(query); containsUintID(got, postID) {
		t.Fatalf("body-changed Post %d remained eligible for v1 semantic recall after invalidation: %v", postID, got)
	}
	assertHydratedEmbedding(query, recentCandidates, nil)

	var changedPost models.Post
	if err := db.First(&changedPost, postID).Error; err != nil {
		t.Fatalf("load body-changed Post: %v", err)
	}
	// This row represents the Consumer's successful v2 projection for the new body.
	if err := db.Create(&models.PostEmbedding{
		PostID: postID, Version: "post_embedding_v2", Model: "devdata-recommendation-lifecycle",
		Dimensions: 2, Embedding: pgvector.NewVector([]float32{0, 1}),
		ContentHash: embeddings.PostEmbeddingContentHash(changedPost.Content), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("write current v2 projection: %v", err)
	}
	query, recentCandidates = loadServingCandidates()
	if !containsUintID(recentCandidateIDs(recentCandidates), postID) {
		t.Fatalf("v2 projection removed Post %d from ordinary recent recall before serving version switch: %v", postID, recentCandidateIDs(recentCandidates))
	}
	if got := loadServingSemanticCandidateIDs(query); containsUintID(got, postID) {
		t.Fatalf("v2 projection made Post %d eligible for v1 semantic recall before serving version switch: %v", postID, got)
	}
	assertHydratedEmbedding(query, recentCandidates, nil)
	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v2"); err != nil {
		t.Fatalf("switch serving version to v2: %v", err)
	}
	query, recentCandidates = loadServingCandidates()
	if !containsUintID(recentCandidateIDs(recentCandidates), postID) {
		t.Fatalf("Post %d with current v2 projection is not eligible for ordinary recent recall after serving version switch: %v", postID, recentCandidateIDs(recentCandidates))
	}
	if got := loadServingSemanticCandidateIDs(query); !containsUintID(got, postID) {
		t.Fatalf("Post %d with current v2 projection is not eligible for v2 semantic recall after serving version switch: %v", postID, got)
	}
	assertHydratedEmbedding(query, recentCandidates, []float32{0, 1})
	post.SourceMetrics.LikeCount++
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(2*time.Minute), post), nil, now.Add(2*time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync non-body Post metadata: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, postID, "post_embedding_v2")
	assertDevDataEmbeddingOutbox(t, db, postID, 1)
}

func containsUintID(ids []uint, want uint) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestDevDataEmbeddingOutboxFailureRollsBackPostAndVectorsIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	post := data.sourcePost(data.Registry.Accounts[0].Key, 1702, now.Add(-time.Hour), "embedding outbox failure A")
	initial := data.snapshot(now, post)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now); err != nil {
		t.Fatalf("seed outbox failure snapshot: %v", err)
	}
	postID := findMirrorMapping(t, db, post.SourcePostID).LocalPostID
	registerDevDataEmbeddingCleanup(t, db, postID, 0)
	addDevDataVersionedEmbeddings(t, db, postID, post.Text)
	createDevDataEmbeddingOutboxFailureTrigger(t, db, postID)

	changed := post
	changed.Text += " body B"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(time.Minute), changed), nil, now.Add(time.Minute), devDataEmbeddingSyncOptions(true)); err == nil {
		t.Fatal("expected the forced Outbox insert failure")
	}
	var stored models.Post
	if err := db.First(&stored, postID).Error; err != nil {
		t.Fatalf("load Post after Outbox failure: %v", err)
	}
	if stored.Content != post.Text {
		t.Fatalf("failed sync changed Post content to %q, want %q", stored.Content, post.Text)
	}
	assertDevDataEmbeddingVersions(t, db, postID, "post_embedding_v1", "post_embedding_v2")
	assertDevDataEmbeddingOutbox(t, db, postID, 0)
}

func TestDevDataPostUpdateFailureRollsBackEmbeddingInvalidationIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	post := data.sourcePost(data.Registry.Accounts[0].Key, 1703, now.Add(-time.Hour), "embedding update failure A")
	initial := data.snapshot(now, post)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, initial, nil, now); err != nil {
		t.Fatalf("seed Post update failure snapshot: %v", err)
	}
	postID := findMirrorMapping(t, db, post.SourcePostID).LocalPostID
	registerDevDataEmbeddingCleanup(t, db, postID, 0)
	addDevDataVersionedEmbeddings(t, db, postID, post.Text)
	createDevDataPostUpdateFailureTrigger(t, db, postID)

	changed := post
	changed.Text += " body B"
	if _, err := SyncSnapshotWithOptions(context.Background(), db, data.Registry, data.snapshot(now.Add(time.Minute), changed), nil, now.Add(time.Minute), devDataEmbeddingSyncOptions(true)); err == nil {
		t.Fatal("expected the forced Post update failure")
	}
	var stored models.Post
	if err := db.First(&stored, postID).Error; err != nil {
		t.Fatalf("load Post after update failure: %v", err)
	}
	if stored.Content != post.Text {
		t.Fatalf("failed sync changed Post content to %q, want %q", stored.Content, post.Text)
	}
	assertDevDataEmbeddingVersions(t, db, postID, "post_embedding_v1", "post_embedding_v2")
	assertDevDataEmbeddingOutbox(t, db, postID, 0)
}

func createDevDataEmbeddingOutboxFailureTrigger(t *testing.T, db *gorm.DB, postID uint) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "devdata_embedding_outbox_fail_" + suffix
	triggerName := "devdata_embedding_outbox_fail_" + suffix
	createFunction := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.aggregate_id = '%d' AND NEW.event_type = '%s' THEN
		RAISE EXCEPTION 'forced DevData embedding Outbox failure';
	END IF;
	RETURN NEW;
END;
$$`, functionName, postID, eventing.EventTypePostEmbeddingRequested)
	if err := db.Exec(createFunction).Error; err != nil {
		t.Fatalf("create Outbox failure function: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON outbox_events", triggerName)).Error; err != nil {
			t.Errorf("drop Outbox failure trigger: %v", err)
		}
		if err := db.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName)).Error; err != nil {
			t.Errorf("drop Outbox failure function: %v", err)
		}
	})
	if err := db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName)).Error; err != nil {
		t.Fatalf("create Outbox failure trigger: %v", err)
	}
}

func createDevDataPostUpdateFailureTrigger(t *testing.T, db *gorm.DB, postID uint) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := "devdata_post_update_fail_" + suffix
	triggerName := "devdata_post_update_fail_" + suffix
	createFunction := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF OLD.id = %d THEN
		RAISE EXCEPTION 'forced DevData Post update failure';
	END IF;
	RETURN NEW;
END;
$$`, functionName, postID)
	if err := db.Exec(createFunction).Error; err != nil {
		t.Fatalf("create Post update failure function: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON posts", triggerName)).Error; err != nil {
			t.Errorf("drop Post update failure trigger: %v", err)
		}
		if err := db.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName)).Error; err != nil {
			t.Errorf("drop Post update failure function: %v", err)
		}
	})
	if err := db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE ON posts FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName)).Error; err != nil {
		t.Fatalf("create Post update failure trigger: %v", err)
	}
}

func TestDevDataTargetedReplacementBodyChangeInvalidatesEmbeddingsIntegration(t *testing.T) {
	db := openDevDataIntegrationDB(t)
	data := newSyncIntegrationData()
	t.Cleanup(func() { cleanupDevDataIntegrationRows(db, data) })
	now := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	fixture := newTargetedReplacementIntegrationFixture(data, now)
	if _, err := SyncSnapshot(context.Background(), db, data.Registry, fixture.Baseline, nil, now); err != nil {
		t.Fatalf("seed replacement baseline: %v", err)
	}

	newAccountSource := fixture.Batch.Accounts[0]
	newUser := models.User{Username: MirrorUsername(fixture.NewKey), Password: "test"}
	if err := db.Create(&newUser).Error; err != nil {
		t.Fatalf("create previously disabled replacement user: %v", err)
	}
	newAccount := models.DevDataMirrorAccount{
		RegistryKey: fixture.NewKey, Platform: "x", SourceUserID: newAccountSource.SourceUserID,
		SourceHandle: newAccountSource.Handle, LocalUserID: newUser.ID, Category: newAccountSource.Category,
		Enabled: false, SourceAvatarURL: newAccountSource.ProfileImageURL, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&newAccount).Error; err != nil {
		t.Fatalf("create previously disabled replacement account: %v", err)
	}
	if err := db.Model(&newAccount).Update("enabled", false).Error; err != nil {
		t.Fatalf("persist disabled replacement account state: %v", err)
	}
	var storedNewAccount models.DevDataMirrorAccount
	if err := db.First(&storedNewAccount, newAccount.ID).Error; err != nil {
		t.Fatalf("reload previously disabled replacement account: %v", err)
	}
	if storedNewAccount.Enabled {
		t.Fatalf("replacement account Enabled=%t, want false", storedNewAccount.Enabled)
	}
	oldContent := "it-marker-" + data.Tag + " replacement embedding A"
	localPost := models.Post{
		Model:    gorm.Model{CreatedAt: fixture.Batch.Posts[0].CreatedAt, UpdatedAt: now},
		AuthorID: newUser.ID, Content: oldContent, Language: "en", Visibility: "public",
	}
	if err := db.Create(&localPost).Error; err != nil {
		t.Fatalf("create mapped replacement Post: %v", err)
	}
	newMapping := models.DevDataMirrorPost{
		Platform: "x", SourcePostID: fixture.Batch.Posts[0].SourcePostID, SourceURL: fixture.Batch.Posts[0].SourceURL,
		MirrorAccountID: newAccount.ID, LocalPostID: localPost.ID, SourceCreatedAt: fixture.Batch.Posts[0].CreatedAt,
		ContentHash: SourceTextContentHash(oldContent), State: models.DevDataMirrorPostStateActive,
		ImportedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&newMapping).Error; err != nil {
		t.Fatalf("create mapped replacement Post identity: %v", err)
	}
	registerDevDataEmbeddingCleanup(t, db, localPost.ID, 0)
	addDevDataVersionedEmbeddings(t, db, localPost.ID, oldContent)

	changed := fixture.Batch
	changedPost := changed.Posts[0]
	changedPost.Text += " body B"
	changed.Posts[0] = changedPost
	if _, err := SyncTargetedReplacement(context.Background(), db, fixture.Current, fixture.Baseline, fixture.OldKey, changed, nil, now.Add(time.Minute), devDataEmbeddingSyncOptions(true)); err != nil {
		t.Fatalf("sync targeted replacement body change: %v", err)
	}
	assertDevDataEmbeddingVersions(t, db, localPost.ID)
	assertDevDataEmbeddingOutbox(t, db, localPost.ID, 1)
	var stored models.Post
	if err := db.First(&stored, localPost.ID).Error; err != nil {
		t.Fatalf("load replaced Post: %v", err)
	}
	if stored.Content != changedPost.Text {
		t.Fatalf("replaced Post content=%q want=%q", stored.Content, changedPost.Text)
	}
}

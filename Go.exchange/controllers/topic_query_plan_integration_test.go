package controllers

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/internal/testdb"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type topicExplainPlan struct {
	Plan topicExplainNode `json:"Plan"`
}

type topicExplainNode struct {
	NodeType  string             `json:"Node Type"`
	IndexName string             `json:"Index Name"`
	TotalCost float64            `json:"Total Cost"`
	PlanRows  float64            `json:"Plan Rows"`
	Plans     []topicExplainNode `json:"Plans"`
}

func TestTopicBucketQueryPlanBoundsSelectiveCandidatesIntegration(t *testing.T) {
	// Two sources out of 64, over 17 days of source times: the current 12h
	// bucket has about 45 matching rows rather than most of the mapping table.
	tx, topic, anchor := openTopicQueryPlanFixture(t, 64, 30)
	for _, after := range []*topicPostCursorV2{nil, {ShuffleKey: 0, PostID: 25000}} {
		query, args, err := topicBucketCandidatesQuery(topic, anchor, 7654321, 0, after, 21)
		if err != nil {
			t.Fatal(err)
		}
		for _, predicate := range []string{
			"mirror_accounts.registry_key IN ?", "mirror_accounts.enabled = TRUE",
			"mirror_posts.state = ?", "mirror_posts.imported_at <= ?",
			"mirror_posts.source_created_at > ?", "mirror_posts.source_created_at <= ?",
			publicPostEligibilitySQL("posts"), "ORDER BY shuffle_key DESC, id DESC LIMIT ?",
		} {
			if !strings.Contains(query, predicate) {
				t.Fatalf("missing bucket query contract %q: %s", predicate, query)
			}
		}
		if strings.Contains(strings.ToUpper(query), "OFFSET") {
			t.Fatal("bucket query must use keyset pagination, not OFFSET")
		}
		if !reflect.DeepEqual(args[1], topic.SourceKeys) || args[2] != "active" ||
			args[3] != anchor || args[4] != anchor.Add(-12*time.Hour) || args[5] != anchor || args[len(args)-1] != 21 {
			t.Fatalf("unexpected source/bucket/anchor/limit arguments: %v", args)
		}
		if after != nil && (!strings.Contains(query, "shuffle_key < ? OR (shuffle_key = ? AND id < ?)") ||
			args[6] != after.ShuffleKey || args[7] != after.ShuffleKey || args[8] != after.PostID) {
			t.Fatal("continuation query lost its shuffle-key/ID cursor bounds")
		}
		planJSON, plan, err := explainTopicQuery(tx, query, args...)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan) != 1 || plan[0].Plan.NodeType != "Limit" || plan[0].Plan.PlanRows > 21 {
			t.Fatalf("bucket query is not limited to a page: %s", planJSON)
		}
		assertTopicSortCandidatesBounded(t, plan[0].Plan, planJSON)
		t.Logf("continuation=%t uses_membership_index=%t estimated_page_rows=%.0f",
			after != nil, topicPlanUsesIndex(plan[0].Plan, "idx_devdata_mirror_posts_account_state"), plan[0].Plan.PlanRows)
	}
}

func assertTopicSortCandidatesBounded(t *testing.T, node topicExplainNode, planJSON []byte) {
	t.Helper()
	if node.NodeType == "Sort" || node.NodeType == "Incremental Sort" {
		for _, input := range node.Plans {
			// Leave room for estimation error, while rejecting a sort of the
			// unfiltered 50,000-row mapping population.
			if input.PlanRows > 200 {
				t.Fatalf("seeded sort is not bounded to selective bucket candidates: %s", planJSON)
			}
		}
	}
	for _, child := range node.Plans {
		assertTopicSortCandidatesBounded(t, child, planJSON)
	}
}

func TestTopicNextBucketQueryPlanUsesMirrorMembershipIndexIntegration(t *testing.T) {
	tx, topic, anchor := openTopicQueryPlanFixture(t, 8, 1)
	var versionNum int
	if err := tx.Raw("SHOW server_version_num").Row().Scan(&versionNum); err != nil {
		t.Fatal(err)
	}
	if major := versionNum / 10000; major != 16 {
		t.Skipf("MAX versus ordered LIMIT comparison requires PostgreSQL 16, got server_version_num=%d", versionNum)
	}

	maxQuery, args, err := topicNextBucketLookupQuery(topic, anchor, 0)
	if err != nil {
		t.Fatal(err)
	}
	maxPlanJSON, maxPlan, err := explainTopicQuery(tx, maxQuery, args...)
	if err != nil {
		t.Fatal(err)
	}
	if len(maxPlan) != 1 || !topicPlanUsesIndex(maxPlan[0].Plan, "idx_devdata_mirror_posts_account_state") {
		t.Fatalf("next-bucket MAX query plan did not use the mirror membership/source-time index: %s", string(maxPlanJSON))
	}

	orderedQuery := strings.Replace(maxQuery,
		"SELECT MAX(mirror_posts.source_created_at)",
		"SELECT mirror_posts.source_created_at", 1)
	if orderedQuery == maxQuery {
		t.Fatal("could not derive ordered Top-1 comparison from the production query")
	}
	orderedQuery = strings.TrimSpace(orderedQuery) + "\nORDER BY mirror_posts.source_created_at DESC\nLIMIT 1"
	orderedPlanJSON, orderedPlan, err := explainTopicQuery(tx, orderedQuery, args...)
	if err != nil {
		t.Fatal(err)
	}
	if len(orderedPlan) != 1 {
		t.Fatalf("ordered Top-1 query returned %d plan roots: %s", len(orderedPlan), string(orderedPlanJSON))
	}
	t.Logf("PostgreSQL 16 MAX plan: total_cost=%.2f plan_rows=%.0f uses_membership_index=%t plan=%s",
		maxPlan[0].Plan.TotalCost, maxPlan[0].Plan.PlanRows,
		topicPlanUsesIndex(maxPlan[0].Plan, "idx_devdata_mirror_posts_account_state"), maxPlanJSON)
	t.Logf("PostgreSQL 16 ordered LIMIT 1 plan: total_cost=%.2f plan_rows=%.0f uses_membership_index=%t plan=%s",
		orderedPlan[0].Plan.TotalCost, orderedPlan[0].Plan.PlanRows,
		topicPlanUsesIndex(orderedPlan[0].Plan, "idx_devdata_mirror_posts_account_state"), orderedPlanJSON)
}

func openTopicQueryPlanFixture(t *testing.T, accountCount, sourceSpacingSeconds int) (*gorm.DB, config.CuratedTopic, time.Time) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("POSTGRES_TEST_DSN is not set")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	t.Cleanup(func() { tx.Rollback() })
	schema := "topic_plan_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := tx.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec("SET LOCAL search_path TO " + schema + ", public").Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE users (id BIGINT PRIMARY KEY, deleted_at TIMESTAMPTZ NULL)",
		"CREATE TABLE posts (id BIGINT PRIMARY KEY, author_id BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL, visibility TEXT NOT NULL, deleted_at TIMESTAMPTZ NULL)",
		"CREATE TABLE devdata_mirror_accounts (id BIGINT PRIMARY KEY, registry_key TEXT NOT NULL, enabled BOOLEAN NOT NULL)",
		"CREATE TABLE devdata_mirror_posts (id BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, mirror_account_id BIGINT NOT NULL, local_post_id BIGINT NOT NULL, source_created_at TIMESTAMPTZ NOT NULL, imported_at TIMESTAMPTZ NOT NULL, state TEXT NOT NULL)",
		"CREATE INDEX idx_devdata_mirror_posts_account_state ON devdata_mirror_posts (mirror_account_id, state, source_created_at DESC, id DESC)",
	} {
		if err := tx.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Exec("INSERT INTO users (id) VALUES (1)").Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`
INSERT INTO devdata_mirror_accounts (id, registry_key, enabled)
SELECT generated, 'topic-plan-' || generated::text, generated <> ?
FROM generate_series(1, ?) AS generated`, accountCount, accountCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`
INSERT INTO posts (id, author_id, created_at, visibility)
SELECT generated, 1, NOW() - make_interval(secs => generated * ?), 'public'
FROM generate_series(1, 50000) AS generated`, sourceSpacingSeconds).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Exec(`
INSERT INTO devdata_mirror_posts (mirror_account_id, local_post_id, source_created_at, imported_at, state)
SELECT ((generated - 1) % ?) + 1, generated,
       NOW() - make_interval(secs => generated * ?), NOW() - INTERVAL '1 second',
       CASE WHEN generated % 11 = 0 THEN 'tombstone' ELSE 'active' END
FROM generate_series(1, 50000) AS generated`, accountCount, sourceSpacingSeconds).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"devdata_mirror_posts", "devdata_mirror_accounts", "posts", "users"} {
		if err := tx.Exec("ANALYZE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	topic := config.CuratedTopic{Slug: "plan", SourceKeys: []string{"topic-plan-1", "topic-plan-2"}}
	return tx, topic, time.Now().UTC()
}

func explainTopicQuery(tx *gorm.DB, query string, args ...interface{}) ([]byte, []topicExplainPlan, error) {
	var planJSON []byte
	if err := tx.Raw("EXPLAIN (FORMAT JSON)\n"+query, args...).Row().Scan(&planJSON); err != nil {
		return nil, nil, err
	}
	var plan []topicExplainPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return planJSON, nil, fmt.Errorf("decode EXPLAIN JSON: %w; output=%s", err, planJSON)
	}
	return planJSON, plan, nil
}

func topicPlanUsesIndex(node topicExplainNode, indexName string) bool {
	if node.IndexName == indexName {
		return true
	}
	for _, child := range node.Plans {
		if topicPlanUsesIndex(child, indexName) {
			return true
		}
	}
	return false
}

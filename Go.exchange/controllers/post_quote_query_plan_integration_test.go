package controllers

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"Go.exchange/models"
	"gorm.io/gorm"
)

func TestPostQuotesPageSeeksCursorAndMatchesLegacyOrderingIntegration(t *testing.T) {
	db := openFollowingTimelineIntegrationDatabase(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	for _, sql := range []string{
		"CREATE TEMP TABLE users (id bigint PRIMARY KEY, deleted_at timestamptz)",
		"CREATE TEMP TABLE posts (id bigint PRIMARY KEY, created_at timestamptz NOT NULL, author_id bigint NOT NULL, quote_post_id bigint, visibility text NOT NULL, deleted_at timestamptz)",
		"INSERT INTO users SELECT g, CASE WHEN g = 3 THEN now() ELSE NULL END FROM generate_series(1,5) g",
		`INSERT INTO posts SELECT g, '2026-01-01'::timestamptz + (g/3)*interval '1 second',
		 1+(g-1)%5, CASE WHEN g<=50000 THEN 900001 WHEN g%37=0 THEN 900002 ELSE NULL END,
		 CASE WHEN g%19=0 THEN 'private' ELSE 'public' END,
		 CASE WHEN g%23=0 THEN now() ELSE NULL END FROM generate_series(1,200000) g`,
		"CREATE INDEX idx_posts_quote ON posts (quote_post_id) WHERE quote_post_id IS NOT NULL",
		"CREATE INDEX idx_posts_search_public_created ON posts (created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility='public'",
		"CREATE INDEX idx_posts_quotes_public_created ON posts (quote_post_id, created_at DESC, id DESC) WHERE deleted_at IS NULL AND visibility='public' AND quote_post_id IS NOT NULL",
		"ANALYZE posts", "ANALYZE users",
	} {
		if err := tx.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, target := range []uint{900001, 900002, 900003} {
		for _, cursorID := range []uint{0, 49999, 10000, 1} {
			var cursor *postRelationCursor
			legacy := publicPostScope(tx.Model(&models.Post{}), base).Where("posts.quote_post_id = ?", target)
			if cursorID != 0 {
				cursor = &postRelationCursor{CreatedAt: base.Add(time.Duration(cursorID/3) * time.Second), ID: cursorID}
				legacy = legacy.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
			}
			var want, got []models.Post
			if err := legacy.Order("created_at DESC, id DESC").Limit(21).Find(&want).Error; err != nil {
				t.Fatal(err)
			}
			query := postQuotesPageQuery(tx, target, base, cursor).Order("created_at DESC, id DESC").Limit(21)
			if err := query.Find(&got).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("target=%d cursor=%d: got %v want %v", target, cursorID, got, want)
			}
			if target == 900001 && cursorID == 10000 {
				statement := query.Session(&gorm.Session{DryRun: true}).Find(&[]models.Post{}).Statement
				var raw []byte
				if err := tx.Raw("EXPLAIN (ANALYZE, FORMAT JSON) "+statement.SQL.String(), statement.Vars...).Row().Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var plans []struct{ Plan quotePageExplainNode }
				if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 {
					t.Fatalf("decode plan: %v %s", err, raw)
				}
				var seek *quotePageExplainNode
				var walk func(*quotePageExplainNode)
				walk = func(node *quotePageExplainNode) {
					if node.IndexName == "idx_posts_quotes_public_created" {
						seek = node
					}
					for i := range node.Plans {
						walk(&node.Plans[i])
					}
				}
				walk(&plans[0].Plan)
				if seek == nil || seek.ActualRows+seek.RowsRemoved > 100 {
					t.Fatalf("deep page scanned too many candidates: %s", raw)
				}
			}
		}
	}
}

type quotePageExplainNode struct {
	IndexName   string                 `json:"Index Name"`
	ActualRows  int                    `json:"Actual Rows"`
	RowsRemoved int                    `json:"Rows Removed by Filter"`
	Plans       []quotePageExplainNode `json:"Plans"`
}

package recommendation

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTrendingOrderProducesPostgresOrderBy(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	const replyFactor = 0.5
	const halfLifeHours = 24.0
	var rows []uint
	result := db.Table("posts").
		Select("posts.id").
		Order(trendingOrder(now, replyFactor, halfLifeHours)).
		Limit(10).
		Find(&rows)
	if result.Error != nil {
		t.Fatalf("build dry-run PostgreSQL query: %v", result.Error)
	}

	statement := result.Statement
	query := statement.SQL.String()
	for _, fragment := range []string{
		"ORDER BY",
		"LN(1 + GREATEST(posts.like_count, 0))",
		"LN(1 + GREATEST(posts.reply_count, 0))",
		"EXP(",
		"EXTRACT(EPOCH FROM (",
		"posts.created_at DESC",
		"posts.id DESC",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("generated SQL does not contain %q:\n%s", fragment, query)
		}
	}
	normalizedQuery := strings.Join(strings.Fields(query), " ")
	if !strings.Contains(normalizedQuery, ") DESC, posts.created_at DESC, posts.id DESC") {
		t.Errorf("generated SQL has incorrect score and tie-break ordering:\n%s", query)
	}

	if len(statement.Vars) < 3 {
		t.Fatalf("bound vars=%#v, want reply factor, UTC now, and half-life", statement.Vars)
	}
	if got, ok := statement.Vars[0].(float64); !ok || got != replyFactor {
		t.Errorf("reply factor var=%#v, want %v", statement.Vars[0], replyFactor)
	}
	gotNow, ok := statement.Vars[1].(time.Time)
	if !ok || !gotNow.Equal(now.UTC()) {
		t.Errorf("now var=%#v, want UTC %v", statement.Vars[1], now.UTC())
	}
	if got, ok := statement.Vars[2].(float64); !ok || got != halfLifeHours {
		t.Errorf("half-life var=%#v, want %v", statement.Vars[2], halfLifeHours)
	}
}

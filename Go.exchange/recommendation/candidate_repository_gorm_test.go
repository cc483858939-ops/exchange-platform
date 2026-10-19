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

func TestRecommendationPublicPostProjectionIncludesQuoteCount(t *testing.T) {
	if !strings.Contains(recommendationPublicPostColumns, "posts.quote_count") {
		t.Fatalf("recommendation public Post projection is missing quote_count: %s", recommendationPublicPostColumns)
	}
}

func TestAuthenticatedCandidateEligibilityFiltersMutedAuthorsBeforeLimit(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	input := CandidateQuery{
		UserID: 73, ServingVersion: "v1", Limit: 20, SoftOnly: true,
		CandidateWindowStart: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		CandidateWindowEnd:   time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC),
		Served:               map[uint]ServedItem{101: {Soft: true}},
	}
	query := repository.eligibilityQuery(db, db.Table("posts").Select("posts.id"), input).
		Order("posts.created_at DESC, posts.id DESC").Limit(input.Limit)
	var rows []uint
	result := query.Find(&rows)
	if result.Error != nil {
		t.Fatalf("build dry-run authenticated query: %v", result.Error)
	}
	sqlText := strings.Join(strings.Fields(result.Statement.SQL.String()), " ")
	muteIndex := strings.Index(sqlText, "user_mutes AS um")
	limitIndex := strings.Index(sqlText, "LIMIT")
	if muteIndex < 0 || limitIndex < 0 || muteIndex >= limitIndex {
		t.Fatalf("mute exclusion is missing or occurs after the limit: %s", sqlText)
	}
	if !strings.Contains(sqlText, "user_mutes AS um") || !strings.Contains(sqlText, "posts.author_id") {
		t.Fatalf("authenticated query is missing author mute anti-join: %s", sqlText)
	}
	if !strings.Contains(sqlText, "posts.id IN") {
		t.Fatalf("soft-served fallback restriction was not retained: %s", sqlText)
	}
}

func TestCandidateEligibilityRequiresCurrentEmbeddingAndWindowBeforeLimit(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	input := CandidateQuery{
		UserID: 73, ServingVersion: "serving-v2", Limit: 20,
		CandidateWindowStart: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		CandidateWindowEnd:   time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC),
	}
	query := repository.eligibilityQuery(db, db.Table("posts").Select("posts.id"), input).
		Order("posts.created_at DESC, posts.id DESC").Limit(input.Limit)
	var rows []uint
	if err := query.Find(&rows).Error; err != nil {
		t.Fatalf("build dry-run candidate eligibility query: %v", err)
	}
	sqlText := strings.Join(strings.Fields(query.Statement.SQL.String()), " ")
	limitIndex := strings.Index(sqlText, "LIMIT")
	for _, fragment := range []string{"eligible_embedding.version =", "eligible_embedding.dimensions > 0", "vector_norm(eligible_embedding.embedding) > 0", "posts.created_at >=", "posts.created_at <="} {
		index := strings.Index(sqlText, fragment)
		if index < 0 || limitIndex < 0 || index >= limitIndex {
			t.Fatalf("candidate query is missing pre-limit eligibility %q: %s", fragment, sqlText)
		}
	}
	var sawServingVersion, sawWindowStart, sawWindowEnd bool
	for _, value := range query.Statement.Vars {
		switch value := value.(type) {
		case string:
			sawServingVersion = sawServingVersion || value == input.ServingVersion
		case time.Time:
			sawWindowStart = sawWindowStart || value.Equal(input.CandidateWindowStart)
			sawWindowEnd = sawWindowEnd || value.Equal(input.CandidateWindowEnd)
		}
	}
	if !sawServingVersion || !sawWindowStart || !sawWindowEnd {
		t.Fatalf("candidate query variables omit serving version or time window: %#v", query.Statement.Vars)
	}
}

func TestPublicCandidateQueriesRemainUnaffectedByMuteRelationships(t *testing.T) {
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: &sql.DB{}}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatalf("open dry-run PostgreSQL database: %v", err)
	}
	input := PublicCandidateQuery{
		ServingVersion: "serving-v2", CandidateWindowStart: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
		CandidateWindowEnd: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC),
	}
	query := publicCandidateQuery(db, input).Order("posts.created_at DESC").Limit(10)
	var rows []uint
	result := query.Find(&rows)
	if result.Error != nil {
		t.Fatalf("build dry-run public candidate query: %v", result.Error)
	}
	if strings.Contains(strings.ToLower(result.Statement.SQL.String()), "user_mutes") {
		t.Fatalf("guest public query was modified by mute filtering: %s", result.Statement.SQL.String())
	}
	sqlText := strings.Join(strings.Fields(result.Statement.SQL.String()), " ")
	limitIndex := strings.Index(sqlText, "LIMIT")
	for _, fragment := range []string{"eligible_embedding.version =", "eligible_embedding.dimensions > 0", "vector_norm(eligible_embedding.embedding) > 0", "posts.created_at >=", "posts.created_at <="} {
		index := strings.Index(sqlText, fragment)
		if index < 0 || limitIndex < 0 || index >= limitIndex {
			t.Fatalf("public query is missing pre-limit eligibility %q: %s", fragment, sqlText)
		}
	}
}

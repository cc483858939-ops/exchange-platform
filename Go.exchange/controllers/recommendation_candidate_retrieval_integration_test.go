package controllers

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func candidateRetrievalArticle(authorID uint, id uint, title string, category string, createdAt time.Time) *models.Article {
	publishedAt := createdAt
	return &models.Article{
		Model:            gorm.Model{ID: id, CreatedAt: createdAt, UpdatedAt: createdAt},
		AuthorID:         authorID,
		Title:            title,
		Content:          title + " content",
		Preview:          title + " preview",
		Category:         category,
		PublicationState: consts.ArticlePublicationStatePublished,
		AnalysisState:    consts.ArticleAnalysisStateCompleted,
		PublishedAt:      &publishedAt,
	}
}

func TestRulesV3PersonalizedCategoryRecallFindsArticleOutsideNewest200Integration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)

	articles := make([]*models.Article, 0, 251)
	for index := 0; index < 250; index++ {
		createdAt := now.Add(-time.Duration(index+1) * time.Minute)
		articles = append(articles, candidateRetrievalArticle(author.ID, 0, "travel-"+strconv.Itoa(index), "travel", createdAt))
	}
	backend := candidateRetrievalArticle(author.ID, 0, "older backend", " Backend ", now.Add(-31*24*time.Hour))
	articles = append(articles, backend)
	if err := db.CreateInBatches(articles, 50).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(articles))
		for _, article := range articles {
			ids = append(ids, article.ID)
		}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	profile := userInterestProfile{
		Categories:              map[string]float64{"backend": 0.9},
		Tags:                    map[string]float64{},
		InteractedArticleIDs:    map[uint]struct{}{},
		PersonalizedSignalCount: 1,
	}
	candidates, err := loadRulesV3Candidates(userID, profile, now.AddDate(0, 0, -90), now, 2)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	var travel *models.Article
	for index := range candidates {
		if candidates[index].ID == backend.ID {
			seen = true
		}
		if travel == nil && candidates[index].Category == "travel" {
			travel = &candidates[index]
		}
	}
	if !seen {
		t.Fatalf("normalized category article %d was not recalled from outside newest 200", backend.ID)
	}
	if travel == nil {
		t.Fatal("unrelated recent travel article was not recalled")
	}
	cfg := normalizedRulesV3RecommendationConfig()
	if scoreRulesV3Article(profile, *backend, now, cfg) <= scoreRulesV3Article(profile, *travel, now, cfg) {
		t.Fatalf("backend score=%v must beat travel score=%v", scoreRulesV3Article(profile, *backend, now, cfg), scoreRulesV3Article(profile, *travel, now, cfg))
	}
	ranked := recommendRulesV3Articles(profile, candidates, now, cfg, 2)
	if len(ranked) == 0 || ranked[0].ID != backend.ID {
		t.Fatalf("ranked=%#v want backend %d first", ranked, backend.ID)
	}
}

func TestRulesV3CandidateScopePreservesExclusionAndReactionOverrideIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	interacted := candidateRetrievalArticle(author.ID, 0, "interacted backend", "backend", now.Add(-time.Minute))
	suppressed := candidateRetrievalArticle(author.ID, 0, "suppressed backend", "backend", now.Add(-2*time.Minute))
	overridden := candidateRetrievalArticle(author.ID, 0, "overridden backend", "backend", now.Add(-3*time.Minute))
	articles := []*models.Article{interacted, suppressed, overridden}
	if err := db.CreateInBatches(articles, 3).Error; err != nil {
		t.Fatal(err)
	}
	negativeAt := now.Add(-time.Hour)
	negativeBehaviors := []models.ArticleBehavior{
		{UserID: userID, ArticleID: suppressed.ID, Action: eventing.RecommendationBehaviorActionNotInterested, Count: 1, LastSeenAt: negativeAt, Active: true},
		{UserID: userID, ArticleID: overridden.ID, Action: eventing.RecommendationBehaviorActionNotInterested, Count: 1, LastSeenAt: negativeAt, Active: true},
	}
	if err := db.Create(&negativeBehaviors).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.ArticleReaction{
		UserID:         userID,
		ArticleID:      overridden.ID,
		Reaction:       0,
		Liked:          false,
		UpdatedAt:      now.Add(-30 * time.Minute),
		StateChangedAt: now.Add(-30 * time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{interacted.ID, suppressed.ID, overridden.ID}
		db.Where("user_id = ? AND article_id IN ?", userID, ids).Delete(&models.ArticleBehavior{})
		db.Where("user_id = ? AND article_id IN ?", userID, ids).Delete(&models.ArticleReaction{})
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	profile := userInterestProfile{
		Categories:              map[string]float64{"backend": 1},
		Tags:                    map[string]float64{},
		InteractedArticleIDs:    map[uint]struct{}{interacted.ID: {}},
		PersonalizedSignalCount: 1,
	}
	candidates, err := loadRulesV3Candidates(userID, profile, now.AddDate(0, 0, -90), now, 3)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		seen[candidate.ID] = struct{}{}
	}
	if _, ok := seen[interacted.ID]; ok {
		t.Fatal("interacted article must be excluded from every recall source")
	}
	if _, ok := seen[suppressed.ID]; ok {
		t.Fatal("active not_interested must suppress matching category")
	}
	if _, ok := seen[overridden.ID]; !ok {
		t.Fatal("later reaction must override not_interested suppression")
	}
}

func TestHydrateRulesV3CandidateArticlesRestoresIDOrderAndPublicScopeIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	base := uint(time.Now().UnixNano() & 0x1fffffff)
	valid5 := candidateRetrievalArticle(author.ID, base+5, "five", "backend", now)
	valid2 := candidateRetrievalArticle(author.ID, base+2, "two", "backend", now)
	valid9 := candidateRetrievalArticle(author.ID, base+9, "nine", "backend", now)
	deleted := candidateRetrievalArticle(author.ID, base+12, "deleted", "backend", now)
	expired := candidateRetrievalArticle(author.ID, base+13, "expired", "backend", now)
	future := candidateRetrievalArticle(author.ID, base+14, "future", "backend", now.Add(time.Hour))
	draft := candidateRetrievalArticle(author.ID, base+15, "draft", "backend", now)
	expiredAt := now.Add(-time.Hour)
	expired.ExpiredAt = &expiredAt
	future.PublishedAt = func() *time.Time {
		value := now.Add(time.Hour)
		return &value
	}()
	draft.PublicationState = "draft"
	articles := []*models.Article{valid2, valid5, valid9, deleted, expired, future, draft}
	if err := db.CreateInBatches(articles, len(articles)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(deleted).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(articles))
		for _, article := range articles {
			ids = append(ids, article.ID)
		}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	ids := []uint{valid5.ID, valid2.ID, valid9.ID, deleted.ID, expired.ID, future.ID, draft.ID}
	got, err := hydrateRulesV3CandidateArticles(ids, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint{valid5.ID, valid2.ID, valid9.ID}
	if len(got) != len(want) {
		t.Fatalf("hydrated=%v want=%v", articleIDs(got), want)
	}
	for index, article := range got {
		if article.ID != want[index] {
			t.Fatalf("hydrated[%d]=%d want=%d; all=%v", index, article.ID, want[index], articleIDs(got))
		}
	}
}

func TestRulesV3CategoryCandidateIDsBindsTypedInterestValuesIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	userID := uint(time.Now().UnixNano() & 0x3fffffff)
	now := time.Now().UTC().Truncate(time.Microsecond)
	lookbackStart := now.AddDate(0, 0, -90)
	author := createArticleIntegrationAuthor(t, db)

	backend := candidateRetrievalArticle(author.ID, 0, "typed backend", " Backend ", now.Add(-10*time.Minute))
	ai := candidateRetrievalArticle(author.ID, 0, "typed ai", "AI", now.Add(-5*time.Minute))
	travel := candidateRetrievalArticle(author.ID, 0, "typed travel", "travel", now.Add(-time.Minute))
	articles := []*models.Article{backend, ai, travel}
	if err := db.CreateInBatches(articles, len(articles)).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := []uint{backend.ID, ai.ID, travel.ID}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	profile := userInterestProfile{
		Categories: map[string]float64{
			"backend": 0.9,
			"ai":      0.5,
		},
		Tags:                    map[string]float64{},
		InteractedArticleIDs:    map[uint]struct{}{},
		PersonalizedSignalCount: 2,
	}
	ids, err := loadRulesV3CategoryCandidateIDs(userID, profile, lookbackStart, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("category ids=%v want exactly backend and ai", ids)
	}
	if ids[0] != backend.ID || ids[1] != ai.ID {
		t.Fatalf("category ids=%v want numeric affinity order [%d %d]", ids, backend.ID, ai.ID)
	}
	for _, id := range ids {
		if id == travel.ID {
			t.Fatalf("unrelated travel article %d was recalled", travel.ID)
		}
	}
}

type candidateRetrievalQueryCounter struct {
	logger.Interface
	mu             sync.Mutex
	articleSelects int
	userSelects    int
}

func (counter *candidateRetrievalQueryCounter) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	normalized := strings.ToLower(sql)
	if !strings.Contains(normalized, "select") {
		return
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	if strings.Contains(normalized, "articles") {
		counter.articleSelects++
	}
	if strings.Contains(normalized, "users") {
		counter.userSelects++
	}
}

func TestRulesV3PersonalizedCandidateQueryBudgetIntegration(t *testing.T) {
	db := openRulesV3IntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	author := createArticleIntegrationAuthor(t, db)
	articles := []*models.Article{
		candidateRetrievalArticle(author.ID, 0, "backend", "backend", now.Add(-time.Minute)),
		candidateRetrievalArticle(author.ID, 0, "travel one", "travel", now.Add(-2*time.Minute)),
		candidateRetrievalArticle(author.ID, 0, "travel two", "travel", now.Add(-3*time.Minute)),
		candidateRetrievalArticle(author.ID, 0, "travel three", "travel", now.Add(-4*time.Minute)),
	}
	if err := db.CreateInBatches(articles, len(articles)).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ids := make([]uint, 0, len(articles))
		for _, article := range articles {
			ids = append(ids, article.ID)
		}
		db.Unscoped().Where("id IN ?", ids).Delete(&models.Article{})
	})

	counter := &candidateRetrievalQueryCounter{Interface: logger.Default.LogMode(logger.Silent)}
	global.Db = db.Session(&gorm.Session{Logger: counter})
	profile := userInterestProfile{
		Categories:              map[string]float64{"backend": 1},
		Tags:                    map[string]float64{},
		InteractedArticleIDs:    map[uint]struct{}{},
		PersonalizedSignalCount: 1,
	}
	if _, err := loadRulesV3Candidates(1, profile, now.AddDate(0, 0, -90), now, 1); err != nil {
		t.Fatal(err)
	}
	counter.mu.Lock()
	articleSelects := counter.articleSelects
	userSelects := counter.userSelects
	counter.mu.Unlock()
	if articleSelects != 4 {
		t.Fatalf("article SELECT budget=%d want=4 (category, recent, popular, hydrate)", articleSelects)
	}
	if userSelects < 1 {
		t.Fatalf("author preload SELECTs=%d want>=1", userSelects)
	}
}

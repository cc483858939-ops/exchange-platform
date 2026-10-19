package recommendation

import (
	"math"
	"os"
	"testing"
	"time"

	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

const recommendationRecallV3TestVersion = "recommendation-recall-v3-test"

func openRecommendationRecallV3IntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := openRecommendationCandidateIntegrationDB(t)
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.PostEmbedding{}); err != nil {
		t.Fatal(err)
	}

	return db
}

func newRecommendationSemanticPost(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time, vector []float32) models.Post {
	t.Helper()
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, title, publishedAt)
	if err := db.Create(&models.PostEmbedding{
		PostID:      article.ID,
		Version:     recommendationRecallV3TestVersion,
		Model:       "recommendation-recall-v3-test",
		Dimensions:  len(vector),
		Embedding:   pgvector.NewVector(vector),
		ContentHash: title,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func cleanupRecommendationRecallV3Data(db *gorm.DB, postIDs, userIDs []uint) {
	if len(postIDs) > 0 {
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
		db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	}
	if len(userIDs) > 0 {
		db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
	}
}

func TestSemanticRecallUsesCandidateWindowAndCosineOrderIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-window-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "semantic-window-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	best := newRecommendationSemanticPost(t, db, author, "semantic-best", now.Add(-time.Hour), []float32{1, 0})
	second := newRecommendationSemanticPost(t, db, author, "semantic-second", now.Add(-2*24*time.Hour), []float32{0.8, 0.6})
	third := newRecommendationSemanticPost(t, db, author, "semantic-third", now.Add(-3*24*time.Hour), []float32{0.6, 0.8})
	fourth := newRecommendationSemanticPost(t, db, author, "semantic-fourth", now.Add(-4*24*time.Hour), []float32{0, 1})
	expired := newRecommendationSemanticPost(t, db, author, "semantic-expired", now.Add(-8*24*time.Hour), []float32{1, 0})
	wrongVersion := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "semantic-wrong-version", now.Add(-30*time.Minute))
	if err := db.Create(&models.PostEmbedding{
		PostID: wrongVersion.ID, Version: "old-semantic-version", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: wrongVersion.Content,
	}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{best.ID, second.ID, third.ID, fourth.ID, expired.ID, wrongVersion.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, postIDs, userIDs) })

	cfg := testDefaultRecommendationConfig()
	cfg.CandidateWindowDays = 7
	profile := Profile{PositiveVector: []float32{1, 0}}
	candidates, err := loadRecommendationSemanticCandidates(db, recommendationRecallV3TestVersion, viewer.ID, profile, map[uint]ServedItem{}, now, cfg, false, 3)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []uint{best.ID, second.ID, third.ID}
	if !equalUintSlices(CandidatePostIDs(candidates), wantIDs) {
		t.Fatalf("semantic candidates=%v, want nearest in-window candidates %v (fourth=%d expired=%d wrong-version=%d)", CandidatePostIDs(candidates), wantIDs, fourth.ID, expired.ID, wrongVersion.ID)
	}
	for index, candidate := range candidates {
		if !candidate.FromSemantic {
			t.Fatalf("candidate %d is not marked semantic: %+v", index, candidate)
		}
		if index > 0 && candidates[index-1].PositiveSemanticSimilarity < candidate.PositiveSemanticSimilarity {
			t.Fatalf("semantic candidates are not ordered by cosine similarity: %#v", candidates)
		}
	}
}

func TestSemanticRecallPreservesRecommendationEligibilityIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-valid-author")
	mutedAuthor := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-muted-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "semantic-eligibility-deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	vector := []float32{1, 0}
	valid := newRecommendationSemanticPost(t, db, validAuthor, "semantic-valid", now.Add(-time.Hour), vector)
	muted := newRecommendationSemanticPost(t, db, mutedAuthor, "semantic-muted-author", now.Add(-90*time.Minute), vector)
	self := newRecommendationSemanticPost(t, db, viewer, "semantic-self", now.Add(-2*time.Hour), vector)
	interacted := newRecommendationSemanticPost(t, db, validAuthor, "semantic-interacted", now.Add(-3*time.Hour), vector)
	notInterested := newRecommendationSemanticPost(t, db, validAuthor, "semantic-not-interested", now.Add(-4*time.Hour), vector)
	served := newRecommendationSemanticPost(t, db, validAuthor, "semantic-served", now.Add(-5*time.Hour), vector)
	deleted := newRecommendationSemanticPost(t, db, deletedAuthor, "semantic-deleted-author", now.Add(-6*time.Hour), vector)
	wrongVersion := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, validAuthor, "semantic-wrong-version", now.Add(-7*time.Hour))
	if err := db.Create(&models.PostEmbedding{
		PostID: wrongVersion.ID, Version: "old-embedding-version", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector(vector), ContentHash: "semantic-wrong-version",
	}).Error; err != nil {
		t.Fatal(err)
	}
	var servingEmbeddingCount int64
	if err := db.Model(&models.PostEmbedding{}).
		Where("post_id = ? AND version = ?", wrongVersion.ID, recommendationRecallV3TestVersion).
		Count(&servingEmbeddingCount).Error; err != nil {
		t.Fatal(err)
	}
	if servingEmbeddingCount != 0 {
		t.Fatalf("wrong-version fixture has %d serving embeddings, want none", servingEmbeddingCount)
	}
	postIDs := []uint{valid.ID, muted.ID, self.ID, interacted.ID, notInterested.ID, served.ID, deleted.ID, wrongVersion.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, mutedAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, postIDs, userIDs) })

	if err := db.Create(&models.PostBehavior{
		UserID: viewer.ID, PostID: notInterested.ID,
		Action: eventing.RecommendationBehaviorActionNotInterested, Active: true, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserMute{MuterID: viewer.ID, MutedID: mutedAuthor.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	profile := Profile{
		PositiveVector:    vector,
		InteractedPostIDs: map[uint]struct{}{interacted.ID: {}},
	}
	servedHistory := map[uint]ServedItem{served.ID: {LastServedAt: now.Add(-time.Minute), Hard: true}}
	cfg := testDefaultRecommendationConfig()
	candidates, err := loadRecommendationSemanticCandidates(db, recommendationRecallV3TestVersion, viewer.ID, profile, servedHistory, now, cfg, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].PostID != valid.ID {
		t.Fatalf("candidates=%#v, want only valid post %d; muted post=%d", candidates, valid.ID, muted.ID)
	}
}

func TestSemanticRecallUnderfillDoesNotEscapeCandidateWindowIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "semantic-underfill-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "semantic-underfill-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	valid := newRecommendationSemanticPost(t, db, author, "semantic-window-valid", now.Add(-24*time.Hour), []float32{0.7, 0.71414286})
	expired := newRecommendationSemanticPost(t, db, author, "semantic-window-expired", now.Add(-8*24*time.Hour), []float32{1, 0})
	future := newRecommendationSemanticPost(t, db, author, "semantic-window-future", now.Add(time.Hour), []float32{1, 0})
	wrongVersion := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "semantic-window-wrong-version", now.Add(-2*time.Hour))
	if err := db.Create(&models.PostEmbedding{
		PostID: wrongVersion.ID, Version: "old-semantic-version", Model: "test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: wrongVersion.Content,
	}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{valid.ID, expired.ID, future.ID, wrongVersion.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, postIDs, userIDs) })

	cfg := testDefaultRecommendationConfig()
	cfg.CandidateWindowDays = 7
	profile := Profile{PositiveVector: []float32{1, 0}}
	candidates, err := loadRecommendationSemanticCandidates(db, recommendationRecallV3TestVersion, viewer.ID, profile, map[uint]ServedItem{}, now, cfg, false, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !equalUintSlices(CandidatePostIDs(candidates), []uint{valid.ID}) {
		t.Fatalf("window-underfilled candidates=%v, want only in-window current-version post %d; expired=%d future=%d wrong-version=%d", CandidatePostIDs(candidates), valid.ID, expired.ID, future.ID, wrongVersion.ID)
	}
	empty, err := loadRecommendationSemanticCandidates(db, "no-current-serving-vector", viewer.ID, profile, map[uint]ServedItem{}, now, cfg, false, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("no-serving-vector candidates=%#v, want empty", empty)
	}
}

func assertUniqueRecommendationPostIDs(t *testing.T, candidates []Candidate) {
	t.Helper()
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate.PostID]; exists {
			t.Fatalf("duplicate article ID=%d in candidates=%#v", candidate.PostID, candidates)
		}
		seen[candidate.PostID] = struct{}{}
	}
}

func newRecommendationTrendingPost(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time, likes, replies int64) models.Post {
	t.Helper()
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, title, publishedAt)
	if err := db.Create(&models.PostEmbedding{
		PostID:      article.ID,
		Version:     recommendationRecallV3TestVersion,
		Model:       "recommendation-recall-v3-test",
		Dimensions:  2,
		Embedding:   pgvector.NewVector([]float32{1, 0}),
		ContentHash: embeddings.PostEmbeddingContentHash(article.Content),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Updates(map[string]interface{}{
		"like_count": likes, "reply_count": replies,
	}).Error; err != nil {
		t.Fatal(err)
	}
	article.LikeCount = likes
	article.ReplyCount = replies
	return article
}

func TestRecommendationTrendingRecallUsesAgeDecayAndPositiveEngagementIntegration(t *testing.T) {
	if os.Getenv("POSTGRES_TEST_DSN") == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "trending-order-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "trending-order-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	oldLifetimeWinner := newRecommendationTrendingPost(t, db, author, "trending-old-lifetime-winner", now.Add(-8*24*time.Hour), 100000, 0)
	olderStrong := newRecommendationTrendingPost(t, db, author, "trending-older-strong", now.Add(-48*time.Hour), 100, 0)
	newerWeak := newRecommendationTrendingPost(t, db, author, "trending-newer-weak", now.Add(-time.Hour), 10, 0)
	zeroEngagement := newRecommendationTrendingPost(t, db, author, "trending-zero-engagement", now.Add(-2*time.Hour), 0, 0)
	postIDs := []uint{oldLifetimeWinner.ID, olderStrong.ID, newerWeak.ID, zeroEngagement.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, postIDs, userIDs) })

	cfg := testDefaultRecommendationConfig()
	cfg.Trending.MaxAgeDays = 7
	cfg.Trending.HalfLifeHours = 24
	cfg.Trending.ReplyFactor = 0
	candidates, err := loadRecommendationTrendingCandidates(db, recommendationRecallV3TestVersion, viewer.ID, Profile{}, map[uint]ServedItem{}, now, cfg, false, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count=%d, want 2; candidates=%#v", len(candidates), candidates)
	}
	if candidates[0].PostID != newerWeak.ID || candidates[1].PostID != olderStrong.ID {
		t.Fatalf("candidates=%#v, want newer weak before older strong", candidates)
	}
	for _, candidate := range candidates {
		if !candidate.FromTrending {
			t.Fatalf("candidate metadata=%#v, want FromTrending=true", candidate)
		}
		if candidate.PostID == oldLifetimeWinner.ID || candidate.PostID == zeroEngagement.ID {
			t.Fatalf("ineligible Trending article returned: %#v", candidate)
		}
	}

	olderRaw := math.Log1p(100) * math.Exp(-math.Ln2*48/cfg.Trending.HalfLifeHours)
	newerRaw := math.Log1p(10) * math.Exp(-math.Ln2*1/cfg.Trending.HalfLifeHours)
	if !(newerRaw > olderRaw) {
		t.Fatalf("test data does not prove decay reorder: newer=%v older=%v", newerRaw, olderRaw)
	}
}

func TestRecommendationTrendingRecallUsesPublishedAtAndIDTieBreakIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "trending-tie-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "trending-tie-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	publishedAt := now.Add(-time.Hour)
	first := newRecommendationTrendingPost(t, db, author, "trending-tie-first", publishedAt, 20, 4)
	second := newRecommendationTrendingPost(t, db, author, "trending-tie-second", publishedAt, 20, 4)
	postIDs := []uint{first.ID, second.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, postIDs, userIDs) })

	cfg := testDefaultRecommendationConfig()
	candidates, err := loadRecommendationTrendingCandidates(db, recommendationRecallV3TestVersion, viewer.ID, Profile{}, map[uint]ServedItem{}, now, cfg, false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].PostID != second.ID || candidates[1].PostID != first.ID {
		t.Fatalf("candidates=%#v, want IDs [%d %d]", candidates, second.ID, first.ID)
	}
}

func TestRecommendationTrendingRecallPreservesEligibilityIntegration(t *testing.T) {
	db := openRecommendationRecallV3IntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "trending-eligibility-viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "trending-eligibility-valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "trending-eligibility-deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	valid := newRecommendationTrendingPost(t, db, validAuthor, "trending-valid", now.Add(-time.Hour), 5, 1)
	self := newRecommendationTrendingPost(t, db, viewer, "trending-self", now.Add(-2*time.Hour), 50, 1)
	notInterested := newRecommendationTrendingPost(t, db, validAuthor, "trending-not-interested", now.Add(-3*time.Hour), 50, 1)
	interacted := newRecommendationTrendingPost(t, db, validAuthor, "trending-interacted", now.Add(-4*time.Hour), 50, 1)
	hardServed := newRecommendationTrendingPost(t, db, validAuthor, "trending-hard-served", now.Add(-5*time.Hour), 50, 1)
	deletedAuthorPost := newRecommendationTrendingPost(t, db, deletedAuthor, "trending-deleted-author", now.Add(-6*time.Hour), 50, 1)
	deletedPost := newRecommendationTrendingPost(t, db, validAuthor, "trending-deleted-post", now.Add(-7*time.Hour), 50, 1)
	postIDs := []uint{valid.ID, self.ID, notInterested.ID, interacted.ID, hardServed.ID, deletedAuthorPost.ID, deletedPost.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() { cleanupRecommendationRecallV3Data(db, postIDs, userIDs) })

	if err := db.Model(&models.PostBehavior{}).Create(&models.PostBehavior{
		UserID: viewer.ID, PostID: notInterested.ID,
		Action: eventing.RecommendationBehaviorActionNotInterested, Active: true, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&deletedPost).Error; err != nil {
		t.Fatal(err)
	}

	cfg := testDefaultRecommendationConfig()
	profile := Profile{InteractedPostIDs: map[uint]struct{}{interacted.ID: {}}}
	served := map[uint]ServedItem{hardServed.ID: {LastServedAt: now.Add(-time.Minute), Hard: true}}
	candidates, err := loadRecommendationTrendingCandidates(db, recommendationRecallV3TestVersion, viewer.ID, profile, served, now, cfg, false, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].PostID != valid.ID {
		t.Fatalf("candidates=%#v, want only valid post %d", candidates, valid.ID)
	}
}

package recommendation

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/embeddingstate"
	"Go.exchange/initialize"
	"Go.exchange/internal/testdb"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

func openRecommendationCandidateIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
	}
	db, err := testdb.Open(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&models.User{},
		&models.UserFollow{},
		&models.UserMute{},
		&models.Post{},
		&models.PostEmbedding{},
		&models.PostBehavior{},
		&models.PostReaction{},
	); err != nil {
		t.Fatal(err)
	}

	originalConfig := config.AppConfig
	config.AppConfig = nil
	t.Cleanup(func() {
		config.AppConfig = originalConfig
	})
	if err := initialize.RunMigrationsWithDB(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	previousServingVersion, err := embeddingstate.LoadServingVersion(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := embeddingstate.SetServingVersion(context.Background(), db, previousServingVersion); err != nil {
			t.Errorf("restore embedding serving version: %v", err)
		}
	})
	return db
}

func newRecommendationCandidateIntegrationUser(t *testing.T, db *gorm.DB, label string) models.User {
	t.Helper()
	user := models.User{
		Username: "recommendation-candidate-" + label + "-" + uuid.NewString(),
		Password: "test",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("user_id = ?", user.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("id = ?", user.ID).Delete(&models.User{})
	})
	return user
}

func newRecommendationCandidateIntegrationPost(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, title, publishedAt)
	embedding := models.PostEmbedding{
		PostID: article.ID, Version: "post_embedding_v1", Model: "recommendation-candidate-test",
		Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}),
		ContentHash: embeddings.PostEmbeddingContentHash(article.Content),
	}
	if err := db.Create(&embedding).Error; err != nil {
		t.Fatal(err)
	}
	return article
}

func newRecommendationCandidateIntegrationPostWithoutEmbedding(t *testing.T, db *gorm.DB, author models.User, title string, publishedAt time.Time) models.Post {
	t.Helper()
	article := models.Post{
		Model:    gorm.Model{CreatedAt: publishedAt, UpdatedAt: publishedAt},
		AuthorID: author.ID, Content: "body", Visibility: "public",
	}
	if err := db.Create(&article).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostEmbedding{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostReaction{})
		db.Unscoped().Where("post_id = ?", article.ID).Delete(&models.PostBehavior{})
		db.Unscoped().Where("id = ?", article.ID).Delete(&models.Post{})
	})
	return article
}

func cleanupRecommendationCandidateIntegrationData(db *gorm.DB, postIDs, userIDs []uint) {
	db.Unscoped().Where("follower_id IN ? OR following_id IN ?", userIDs, userIDs).Delete(&models.UserFollow{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostEmbedding{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostReaction{})
	db.Unscoped().Where("post_id IN ?", postIDs).Delete(&models.PostBehavior{})
	db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	db.Unscoped().Where("id IN ?", userIDs).Delete(&models.User{})
}

func TestLoadRecommendationCandidateSetUsesEqualRRFFusionIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "rrf-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "rrf-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	article := newRecommendationCandidateIntegrationPost(t, db, author, "rrf", now)
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&models.Post{}).Where("id = ?", article.ID).Update("like_count", 1).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{article.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	candidateSet, err := serviceCandidateSetFromDB(db, "post_embedding_v1", viewer.ID, Profile{}, ServedHistory{}, now, testDefaultRecommendationConfig(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidateSet.Candidates) != 1 || candidateSet.Candidates[0].PostID != article.ID {
		t.Fatalf("candidate set=%#v, want one RRF-fused post", candidateSet)
	}
	candidate := candidateSet.Candidates[0]
	if !candidate.FromFollowing || !candidate.FromRecent || !candidate.FromTrending || candidate.SourceCount != 3 || candidate.FollowingRank != 1 || candidate.RecentRank != 1 || candidate.TrendingRank != 1 {
		t.Fatalf("fused candidate metadata=%#v", candidate)
	}
	if want := 3.0 / 61; math.Abs(candidate.FusionScore-want) > 1e-12 {
		t.Fatalf("fusion score=%v want=%v", candidate.FusionScore, want)
	}
}

func TestRecommendationCandidateSourcesRequireServingEmbeddingAcrossAllSourcesIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "serving-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "serving-author")
	now := time.Now().UTC().Truncate(time.Microsecond)
	noEmbedding := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "no-embedding", now.Add(-4*time.Minute))
	v1Only := newRecommendationCandidateIntegrationPost(t, db, author, "v1-only", now.Add(-3*time.Minute))
	v2Only := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "v2-only", now.Add(-2*time.Minute))
	v1AndV2 := newRecommendationCandidateIntegrationPost(t, db, author, "v1-and-v2", now.Add(-time.Minute))
	zeroVectorPost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "zero-vector", now.Add(-30*time.Second))
	addRecommendationCandidateVersion(t, db, v2Only, "post_embedding_v2")
	addRecommendationCandidateVersion(t, db, v1AndV2, "post_embedding_v2")
	if err := db.Create(&models.PostEmbedding{
		PostID: zeroVectorPost.ID, Version: "post_embedding_v1", Model: "candidate-zero-vector-test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{0, 0}), ContentHash: embeddings.PostEmbeddingContentHash(zeroVectorPost.Content),
	}).Error; err == nil {
		t.Fatal("database accepted a zero-vector recommendation fixture")
	}
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{noEmbedding.ID, v1Only.ID, v2Only.ID, v1AndV2.ID, zeroVectorPost.ID}
	userIDs := []uint{viewer.ID, author.ID}
	registryKey := "serving-version-" + uuid.NewString()
	account := createOnboardingInterestMirrorAccount(t, db, author, registryKey)
	var mirrorPostIDs []uint
	for _, post := range []models.Post{noEmbedding, v1Only, v2Only, v1AndV2, zeroVectorPost} {
		addRecommendationCandidateMirrorPost(t, db, account, post, &mirrorPostIDs)
		if err := db.Model(&models.Post{}).Where("id = ?", post.ID).Update("like_count", 1).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := db.Unscoped().Where("local_post_id IN ?", mirrorPostIDs).Delete(&models.DevDataMirrorPost{}).Error; err != nil {
			t.Errorf("delete candidate source mappings: %v", err)
		}
		if err := db.Unscoped().Where("id = ?", account.ID).Delete(&models.DevDataMirrorAccount{}).Error; err != nil {
			t.Errorf("delete candidate source account: %v", err)
		}
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	profile := Profile{PositiveVector: []float32{1, 0}}
	cfg := testDefaultRecommendationConfig()
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	allIDs := []uint{noEmbedding.ID, v1Only.ID, v2Only.ID, v1AndV2.ID, zeroVectorPost.ID}
	allNonSemanticIDs := []uint{zeroVectorPost.ID, v1AndV2.ID, v2Only.ID, v1Only.ID, noEmbedding.ID}
	versions := []struct {
		serving string
		want    []uint
	}{{"post_embedding_v1", []uint{v1AndV2.ID, v1Only.ID}}, {"post_embedding_v2", []uint{v1AndV2.ID, v2Only.ID}}}
	for _, tc := range versions {
		t.Run(tc.serving, func(t *testing.T) {
			query := candidateQueryForTest(tc.serving, viewer.ID, profile, nil, now, cfg, false, 10)
			assertIDs := func(source string, got []Candidate, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s candidates: %v", source, err)
				}
				if !equalCandidateIDs(candidateIDs(got), tc.want) {
					t.Fatalf("%s candidates=%v, want %v", source, candidateIDs(got), tc.want)
				}
			}
			following, err := repository.LoadFollowingCandidates(context.Background(), query)
			if err != nil || !equalCandidateIDs(candidateIDs(following), allNonSemanticIDs) {
				t.Fatalf("following candidates=%v err=%v, want all public posts independent of embeddings %v", candidateIDs(following), err, allNonSemanticIDs)
			}
			recent, err := repository.LoadRecentCandidates(context.Background(), query)
			if err != nil || !equalCandidateIDs(candidateIDs(recent), allNonSemanticIDs) {
				t.Fatalf("recent candidates=%v err=%v, want all public posts independent of embeddings %v", candidateIDs(recent), err, allNonSemanticIDs)
			}
			trending, err := repository.LoadTrendingCandidates(context.Background(), query)
			if err != nil || !equalCandidateIDs(candidateIDs(trending), allNonSemanticIDs) {
				t.Fatalf("trending candidates=%v err=%v, want all public posts independent of embeddings %v", candidateIDs(trending), err, allNonSemanticIDs)
			}
			semantic, err := repository.LoadSemanticCandidates(context.Background(), query)
			assertIDs("semantic", semantic, err)
			for _, candidate := range semantic {
				if !candidate.FromSemantic {
					t.Fatalf("semantic candidate lacks source flag: %#v", candidate)
				}
			}

			interestQuery := query
			interestQuery.OnboardingInterestSourceKeys = []string{registryKey}
			interestQuery.OnboardingInterestLimit = 10
			interest, err := repository.LoadRecentCandidates(context.Background(), interestQuery)
			if err != nil || !equalCandidateIDs(candidateIDs(interest), allNonSemanticIDs) {
				t.Fatalf("onboarding interest candidates=%v err=%v, want all public posts independent of embeddings %v", candidateIDs(interest), err, allNonSemanticIDs)
			}
			for _, candidate := range interest {
				if !candidate.FromOnboardingInterest {
					t.Fatalf("onboarding candidate lacks source flag: %#v", candidate)
				}
			}

			publicQuery := PublicCandidateQuery{
				ServingVersion: tc.serving, Now: now, Range: query.Range, RecentCutoff: query.RecentCutoff, Limit: 10,
				TrendingCutoff: now.Add(-3 * 24 * time.Hour), TrendingReplyFactor: 1.5, TrendingHalfLifeHours: 12,
			}
			publicRecent, err := repository.LoadPublicRecentCandidates(context.Background(), publicQuery)
			if err != nil || !equalCandidateIDs(candidateIDs(publicRecent), allNonSemanticIDs) {
				t.Fatalf("public recent candidates=%v err=%v, want all public posts independent of embeddings %v", candidateIDs(publicRecent), err, allNonSemanticIDs)
			}
			publicTrending, err := repository.LoadPublicTrendingCandidates(context.Background(), publicQuery)
			if err != nil || !equalCandidateIDs(candidateIDs(publicTrending), allNonSemanticIDs) {
				t.Fatalf("public trending candidates=%v err=%v, want all public posts independent of embeddings %v", candidateIDs(publicTrending), err, allNonSemanticIDs)
			}

			hydrationCandidates := make([]Candidate, 0, len(allIDs))
			for _, postID := range allIDs {
				hydrationCandidates = append(hydrationCandidates, Candidate{PostID: postID})
			}
			hydrated, err := repository.HydrateCandidates(context.Background(), query, hydrationCandidates)
			if err != nil {
				t.Fatalf("hydrate candidates: %v", err)
			}
			gotHydrated := make([]uint, 0, len(hydrated))
			gotVectors := make(map[uint][]float32, len(hydrated))
			for _, item := range hydrated {
				gotHydrated = append(gotHydrated, item.Post.ID)
				gotVectors[item.Post.ID] = item.Embedding
			}
			if !equalCandidateIDs(gotHydrated, allIDs) {
				t.Fatalf("hydrated candidates=%v, want all eligible candidates %v", gotHydrated, allIDs)
			}
			currentVersionIDs := map[uint]bool{v1Only.ID: tc.serving == "post_embedding_v1", v2Only.ID: tc.serving == "post_embedding_v2", v1AndV2.ID: true}
			for _, postID := range allIDs {
				vector := gotVectors[postID]
				if currentVersionIDs[postID] {
					if !ValidEmbeddingVector(vector) {
						t.Fatalf("post %d should hydrate the current serving vector %q, got %v", postID, tc.serving, vector)
					}
				} else if vector != nil {
					t.Fatalf("post %d must not fall back to a non-serving vector %q: %v", postID, tc.serving, vector)
				}
			}

			softQuery := query
			softQuery.SoftOnly = true
			softQuery.Served = make(ServedHistory, len(allIDs))
			for _, postID := range allIDs {
				softQuery.Served[postID] = ServedItem{PostID: postID, Soft: true, LastServedAt: now.Add(-time.Hour)}
			}
			soft, err := repository.LoadRecentCandidates(context.Background(), softQuery)
			if err != nil || !equalCandidateIDs(candidateIDs(soft), allNonSemanticIDs) {
				t.Fatalf("soft recent candidates=%v err=%v, want all soft-served posts %v", candidateIDs(soft), err, allNonSemanticIDs)
			}
		})
	}

	windowCfg := cfg
	windowCfg.PrimaryWindowDays = 5
	windowCfg.Trending.MaxAgeDays = 10
	windowStart, windowEnd := config.RecommendationPrimaryWindowBounds(now, windowCfg.PrimaryWindowDays)
	boundaryPost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "window-boundary", windowStart)
	currentPost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "window-current", windowEnd)
	olderPost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "window-older", windowStart.Add(-time.Second))
	futurePost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "window-future", windowEnd.Add(time.Second))
	windowPosts := []models.Post{boundaryPost, currentPost, olderPost, futurePost}
	for _, post := range windowPosts {
		addRecommendationCandidateVersion(t, db, post, "post_embedding_v1")
		addRecommendationCandidateVersion(t, db, post, "post_embedding_v2")
		addRecommendationCandidateMirrorPost(t, db, account, post, &mirrorPostIDs)
		if err := db.Model(&models.Post{}).Where("id = ?", post.ID).Update("like_count", 1).Error; err != nil {
			t.Fatal(err)
		}
		postIDs = append(postIDs, post.ID)
	}
	excludedInitial := make(map[uint]struct{}, len(allIDs))
	for _, postID := range allIDs {
		excludedInitial[postID] = struct{}{}
	}
	windowIDs := []uint{boundaryPost.ID, currentPost.ID, olderPost.ID, futurePost.ID}
	for _, servingVersion := range []string{"post_embedding_v1", "post_embedding_v2"} {
		t.Run("five-day-window/"+servingVersion, func(t *testing.T) {
			query := candidateQueryForTest(servingVersion, viewer.ID, profile, nil, now, windowCfg, false, 10)
			query.InteractedPostIDs = excludedInitial
			assertWindowIDs := func(source string, candidates []Candidate, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("%s candidates: %v", source, err)
				}
				if got := candidateIDs(candidates); !equalCandidateIDs(got, []uint{currentPost.ID, boundaryPost.ID}) {
					t.Fatalf("%s candidates=%v, want inclusive window boundary Posts [%d %d] (%s..%s)", source, got, currentPost.ID, boundaryPost.ID, windowStart, windowEnd)
				}
			}

			following, err := repository.LoadFollowingCandidates(context.Background(), query)
			assertWindowIDs("following", following, err)
			recent, err := repository.LoadRecentCandidates(context.Background(), query)
			assertWindowIDs("recent", recent, err)
			trending, err := repository.LoadTrendingCandidates(context.Background(), query)
			assertWindowIDs("trending", trending, err)
			semantic, err := repository.LoadSemanticCandidates(context.Background(), query)
			assertWindowIDs("semantic", semantic, err)

			interestQuery := query
			interestQuery.OnboardingInterestSourceKeys = []string{registryKey}
			interestQuery.OnboardingInterestLimit = 10
			interest, err := repository.LoadRecentCandidates(context.Background(), interestQuery)
			assertWindowIDs("onboarding interest", interest, err)
			if !interest[0].FromOnboardingInterest {
				t.Fatalf("boundary candidate lacks onboarding-interest provenance: %#v", interest[0])
			}

			publicQuery := PublicCandidateQuery{
				ServingVersion: servingVersion, Now: now, Range: CandidateRangeRecent,
				RecentCutoff: windowStart, Limit: 10, ExcludedPostIDs: excludedInitial,
				TrendingCutoff: now.AddDate(0, 0, -windowCfg.Trending.MaxAgeDays), TrendingReplyFactor: 1.5, TrendingHalfLifeHours: 12,
			}
			publicRecent, err := repository.LoadPublicRecentCandidates(context.Background(), publicQuery)
			assertWindowIDs("public recent", publicRecent, err)
			publicTrending, err := repository.LoadPublicTrendingCandidates(context.Background(), publicQuery)
			assertWindowIDs("public trending", publicTrending, err)

			softQuery := query
			softQuery.SoftOnly = true
			softQuery.Served = make(ServedHistory, len(windowIDs))
			for _, postID := range windowIDs {
				softQuery.Served[postID] = ServedItem{PostID: postID, Soft: true, LastServedAt: now.Add(-time.Hour)}
			}
			softRecent, err := repository.LoadRecentCandidates(context.Background(), softQuery)
			assertWindowIDs("soft recent", softRecent, err)

			hydrationCandidates := []Candidate{
				{PostID: olderPost.ID}, {PostID: boundaryPost.ID}, {PostID: futurePost.ID}, {PostID: currentPost.ID},
			}
			hydrated, err := repository.HydrateCandidates(context.Background(), query, hydrationCandidates)
			if err != nil {
				t.Fatalf("hydrate five-day candidates: %v", err)
			}
			if len(hydrated) != 2 || hydrated[0].Post.ID != boundaryPost.ID || hydrated[1].Post.ID != currentPost.ID ||
				!ValidEmbeddingVector(hydrated[0].Embedding) || !ValidEmbeddingVector(hydrated[1].Embedding) {
				t.Fatalf("hydrated candidates=%+v, want valid window Posts [%d %d] in candidate order", hydrated, boundaryPost.ID, currentPost.ID)
			}

			semanticRacePost := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "snapshot-semantic-race", windowEnd.Add(-time.Second))
			addRecommendationCandidateVersion(t, db, semanticRacePost, servingVersion)
			if err := db.Unscoped().Where("post_id = ? AND version = ?", currentPost.ID, servingVersion).Delete(&models.PostEmbedding{}).Error; err != nil {
				t.Fatalf("remove serving embedding before snapshot revalidation: %v", err)
			}
			if err := db.Unscoped().Where("post_id = ? AND version = ?", semanticRacePost.ID, servingVersion).Delete(&models.PostEmbedding{}).Error; err != nil {
				t.Fatalf("remove recalled semantic embedding before snapshot revalidation: %v", err)
			}
			snapshotCandidates, contextEmbeddings, err := repository.LoadSnapshotPage(
				context.Background(), query,
				[]Candidate{
					{PostID: boundaryPost.ID}, {PostID: currentPost.ID},
					{PostID: semanticRacePost.ID, FromSemantic: true, PositiveSemanticSimilarity: .99},
				}, []uint{currentPost.ID},
			)
			if err != nil {
				t.Fatalf("revalidate snapshot candidates after embedding removal: %v", err)
			}
			if len(snapshotCandidates) != 2 || snapshotCandidates[0].Post.ID != boundaryPost.ID || snapshotCandidates[1].Post.ID != currentPost.ID {
				t.Fatalf("snapshot candidates=%+v, want non-semantic posts %d and %d while dropping stale semantic post %d", snapshotCandidates, boundaryPost.ID, currentPost.ID, semanticRacePost.ID)
			}
			if snapshotCandidates[1].Embedding != nil {
				t.Fatalf("snapshot candidate without current vector has embedding=%v, want nil", snapshotCandidates[1].Embedding)
			}
			if _, stillEligibleContext := contextEmbeddings[currentPost.ID]; stillEligibleContext {
				t.Fatalf("snapshot retained context vector after serving embedding removal for Post %d", currentPost.ID)
			}
			addRecommendationCandidateVersion(t, db, currentPost, servingVersion)
			addRecommendationCandidateVersion(t, db, semanticRacePost, servingVersion)
		})
	}
}

func addRecommendationCandidateVersion(t *testing.T, db *gorm.DB, post models.Post, version string) {
	t.Helper()
	if err := db.Create(&models.PostEmbedding{
		PostID: post.ID, Version: version, Model: "candidate-version-test", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(post.Content),
	}).Error; err != nil {
		t.Fatalf("create %s embedding for Post %d: %v", version, post.ID, err)
	}
}

type recommendationCandidateDBServingVersionProvider struct {
	db *gorm.DB
}

func (provider recommendationCandidateDBServingVersionProvider) LoadServingVersion(ctx context.Context) (string, error) {
	return embeddingstate.LoadServingVersion(ctx, provider.db)
}

func TestRecommendationServeKeepsEmbeddingOptionalAcrossServingVersionSwitchesIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "version-switch-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "version-switch-author")
	now := time.Now().UTC().Truncate(time.Microsecond)
	noEmbedding := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "no-embedding", now.Add(-3*time.Minute))
	v1Only := newRecommendationCandidateIntegrationPost(t, db, author, "v1-only", now.Add(-2*time.Minute))
	v2Only := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "v2-only", now.Add(-time.Minute))
	addRecommendationCandidateVersion(t, db, v2Only, "post_embedding_v2")
	postIDs := []uint{noEmbedding.ID, v1Only.ID, v2Only.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() {
		if err := db.Unscoped().Where("user_id = ?", viewer.ID).Delete(&models.UserRecoProfile{}).Error; err != nil {
			t.Errorf("delete version-switch profile: %v", err)
		}
		if err := db.Unscoped().Where("user_id = ?", viewer.ID).Delete(&models.UserRecoProfileDirty{}).Error; err != nil {
			t.Errorf("delete version-switch dirty profile: %v", err)
		}
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	for index, postID := range postIDs {
		if err := db.Model(&models.Post{}).Where("id = ?", postID).Update("like_count", int64(index+1)).Error; err != nil {
			t.Fatal(err)
		}
	}

	cfg := testDefaultRecommendationConfig()
	cfg.Snapshot.Enabled = false
	positive := pgvector.NewVector([]float32{1, 0})
	if err := db.Create(&models.UserRecoProfile{
		UserID: viewer.ID, ProfileVersion: MaterializedProfileVersion,
		ProfileConfigHash: ProfileConfigHash(cfg, "post_embedding_v1"), EmbeddingVersion: "post_embedding_v1",
		Dimensions: 2, PositiveVector: &positive, PositiveSignalCount: 1, PersonalizedSignalCount: 1,
		ComputedAt: now.Add(-time.Minute), NextRebuildAt: now.Add(time.Hour), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	candidates, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := NewGormProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles},
		ServingVersions:  recommendationCandidateDBServingVersionProvider{db: db},
	}, ServiceConfig{Recommendation: cfg})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(kind ViewerKind, userID uint, label string) ServeResult {
		t.Helper()
		viewer := Viewer{Kind: kind, UserID: userID}
		if kind == ViewerGuest {
			viewer.GuestSessionID = "guest-" + label
		}
		result, err := service.Serve(context.Background(), ServeRequest{
			Viewer: viewer, Limit: len(postIDs), RequestID: "candidate-version-" + label + "-" + uuid.NewString(), Now: now,
		})
		if err != nil {
			t.Fatalf("serve %s: %v", label, err)
		}
		return result
	}
	assertNoEmbeddingSelected := func(label string, result ServeResult) {
		t.Helper()
		for _, selected := range result.Selected {
			if selected.Post.ID == noEmbedding.ID {
				if selected.Embedding != nil || selected.Breakdown.PositiveSemantic != 0 || selected.Breakdown.NegativeSemantic != 0 ||
					selected.Breakdown.SemanticComponent != 0 || selected.ExplorationSemantic != 0 {
					t.Fatalf("%s selected missing-vector post has semantic data: %+v", label, selected)
				}
				return
			}
		}
		t.Fatalf("%s result omitted eligible post without an embedding: selected=%v", label, selectedPostIDs(result.Selected))
	}

	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v1"); err != nil {
		t.Fatal(err)
	}
	v1 := serve(ViewerAuthenticated, viewer.ID, "v1")
	if v1.Profile.ProfileStatus != ProfileStatusHit || v1.FreshCandidateSummary.SemanticCount == 0 || len(v1.Selected) != len(postIDs) {
		t.Fatalf("v1 serve profile=%s semantic=%d selected=%v", v1.Profile.ProfileStatus, v1.FreshCandidateSummary.SemanticCount, selectedPostIDs(v1.Selected))
	}
	assertNoEmbeddingSelected("v1", v1)

	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v2"); err != nil {
		t.Fatal(err)
	}
	v2 := serve(ViewerAuthenticated, viewer.ID, "v2")
	if v2.Profile.ProfileStatus != ProfileStatusIncompatible || len(v2.Profile.PositiveVector) != 0 || v2.FreshCandidateSummary.SemanticCount != 0 || len(v2.Selected) != len(postIDs) {
		t.Fatalf("v2 switch reused old profile or lost non-semantic candidates: profile=%+v semantic=%d selected=%v", v2.Profile, v2.FreshCandidateSummary.SemanticCount, selectedPostIDs(v2.Selected))
	}
	assertNoEmbeddingSelected("v2", v2)

	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v1"); err != nil {
		t.Fatal(err)
	}
	v1Again := serve(ViewerAuthenticated, viewer.ID, "v1-again")
	if v1Again.Profile.ProfileStatus != ProfileStatusHit || v1Again.FreshCandidateSummary.SemanticCount == 0 || len(v1Again.Selected) != len(postIDs) {
		t.Fatalf("v1 rollback profile=%s semantic=%d selected=%v", v1Again.Profile.ProfileStatus, v1Again.FreshCandidateSummary.SemanticCount, selectedPostIDs(v1Again.Selected))
	}
	assertNoEmbeddingSelected("v1 rollback", v1Again)

	guest := serve(ViewerGuest, 0, "guest")
	if len(guest.Selected) != len(postIDs) {
		t.Fatalf("guest selected=%v, want all public candidates %v", selectedPostIDs(guest.Selected), postIDs)
	}
	assertNoEmbeddingSelected("guest", guest)
}

func TestRecommendationServeExcludesCanonicalInteractionAcrossServingVersionSwitchIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "interaction-version-switch-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "interaction-version-switch-author")
	now := time.Now().UTC().Truncate(time.Microsecond)
	interacted := newRecommendationCandidateIntegrationPost(t, db, author, "interacted", now.AddDate(0, 0, -400))
	withoutEmbedding := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "without-embedding", now.AddDate(0, 0, -401))
	postIDs := []uint{interacted.ID, withoutEmbedding.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() {
		if err := db.Unscoped().Where("user_id = ? AND post_id IN ?", viewer.ID, postIDs).Delete(&models.UserPostRecoState{}).Error; err != nil {
			t.Errorf("delete canonical interaction fixture: %v", err)
		}
		if err := db.Unscoped().Where("user_id = ?", viewer.ID).Delete(&models.UserRecoProfileDirty{}).Error; err != nil {
			t.Errorf("delete dirty profile fixture: %v", err)
		}
		if err := db.Unscoped().Where("user_id = ?", viewer.ID).Delete(&models.UserRecoProfile{}).Error; err != nil {
			t.Errorf("delete materialized profile fixture: %v", err)
		}
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserPostRecoState{
		UserID: viewer.ID, PostID: interacted.ID, Interacted: true,
		CanonicalVersion: CanonicalOutcomeVersion, RebuiltAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	cfg := testDefaultRecommendationConfig()
	cfg.Snapshot.Enabled = false
	positive := pgvector.NewVector([]float32{1, 0})
	if err := db.Create(&models.UserRecoProfile{
		UserID: viewer.ID, ProfileVersion: MaterializedProfileVersion,
		ProfileConfigHash: ProfileConfigHash(cfg, "post_embedding_v1"), EmbeddingVersion: "post_embedding_v1",
		Dimensions: 2, PositiveVector: &positive, PositiveSignalCount: 1, PersonalizedSignalCount: 1,
		ComputedAt: now.Add(-time.Minute), NextRebuildAt: now.Add(time.Hour), UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	candidates, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := NewGormProfileRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, Profiles: profiles},
		ServingVersions:  recommendationCandidateDBServingVersionProvider{db: db},
	}, ServiceConfig{Recommendation: cfg})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(label string) ServeResult {
		t.Helper()
		result, err := service.Serve(context.Background(), ServeRequest{
			Viewer: Viewer{Kind: ViewerAuthenticated, UserID: viewer.ID}, Limit: len(postIDs),
			RequestID: "interaction-version-switch-" + label + "-" + uuid.NewString(), Now: now,
		})
		if err != nil {
			t.Fatalf("serve %s: %v", label, err)
		}
		return result
	}
	assertOnlyUninteractedWithoutEmbedding := func(label string, result ServeResult) {
		t.Helper()
		if !equalCandidateIDs(selectedPostIDs(result.Selected), []uint{withoutEmbedding.ID}) {
			t.Fatalf("%s selected=%v, want only eligible no-vector post %d", label, selectedPostIDs(result.Selected), withoutEmbedding.ID)
		}
		if result.Selected[0].Embedding != nil {
			t.Fatalf("%s no-vector post has embedding %v", label, result.Selected[0].Embedding)
		}
	}

	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v1"); err != nil {
		t.Fatal(err)
	}
	v1 := serve("v1")
	if v1.Profile.ProfileStatus != ProfileStatusHit || !v1.Profile.MaterializedInteractionsReady {
		t.Fatalf("v1 profile=%+v, want compatible materialized profile", v1.Profile)
	}
	assertOnlyUninteractedWithoutEmbedding("v1", v1)

	if err := embeddingstate.SetServingVersion(context.Background(), db, "post_embedding_v2"); err != nil {
		t.Fatal(err)
	}
	v2 := serve("v2")
	if v2.Profile.ProfileStatus != ProfileStatusIncompatible || v2.Profile.MaterializedInteractionsReady || len(v2.Profile.PositiveVector) != 0 {
		t.Fatalf("v2 profile=%+v, want incompatible profile without a reused vector", v2.Profile)
	}
	assertOnlyUninteractedWithoutEmbedding("v2", v2)

	var persisted models.UserRecoProfile
	if err := db.Where("user_id = ?", viewer.ID).First(&persisted).Error; err != nil {
		t.Fatalf("load profile after v2 serving: %v", err)
	}
	if persisted.EmbeddingVersion != "post_embedding_v1" {
		t.Fatalf("profile was rebuilt during v2 serving: embedding version=%q", persisted.EmbeddingVersion)
	}
}

func TestHistoricalSnapshotPagesRetainUnvectorizedPostsIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "historical-snapshot-author")
	now := time.Now().UTC().Truncate(time.Second)
	posts := []models.Post{
		newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "historical-snapshot-400d", now.AddDate(0, 0, -400)),
		newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "historical-snapshot-401d", now.AddDate(0, 0, -401)),
		newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "historical-snapshot-402d", now.AddDate(0, 0, -402)),
	}
	cfg := testDefaultRecommendationConfig()
	cfg.Snapshot = config.RecommendationSnapshotConfig{
		Enabled: true, TTLSeconds: 120, MaxCandidates: 10, PreselectPages: 1,
		DefaultPageSize: 1, MaxPageSize: 1,
	}
	cfg.HistoricalBackfill = config.RecommendationHistoricalBackfillConfig{BatchSize: 2, MaxScanPerRequest: 2}
	cfg.OutOfNetworkMinRatio = 0
	cfg.Diversity.Enabled = false
	cfg.Exploration.Ratio = 0
	candidates, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	history := &serviceTestHistoryStore{}
	service, err := NewService(ServiceDependencies{
		DataDependencies: DataDependencies{Candidates: candidates, History: history},
		ServingVersions:  recommendationCandidateDBServingVersionProvider{db: db},
		Snapshots:        newMemorySnapshotStore(),
	}, ServiceConfig{
		Recommendation: cfg, SnapshotSigningKey: []byte(strings.Repeat("historical-snapshot-signing-key-", 2)),
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Kind: ViewerGuest, GuestSessionID: uuid.NewString()}
	seen := make(map[uint]struct{}, len(posts))
	request := ServeRequest{Viewer: viewer, Limit: 1, RequestID: uuid.NewString(), Now: now}
	for pageIndex := 0; pageIndex < len(posts)+2; pageIndex++ {
		result, err := service.Serve(context.Background(), request)
		if err != nil {
			t.Fatalf("serve historical page %d: %v", pageIndex, err)
		}
		for _, item := range result.Selected {
			if item.Candidate.RecallTier != RecallTierHistoricalUnseen || item.Embedding != nil {
				t.Fatalf("page %d historical item lost tier or required an embedding: %+v", pageIndex, item)
			}
			if _, duplicate := seen[item.Post.ID]; duplicate {
				t.Fatalf("page %d repeated historical post %d", pageIndex, item.Post.ID)
			}
			seen[item.Post.ID] = struct{}{}
		}
		if err := service.(ServingFinalizer).FinalizeServing(context.Background(), result, result.Selected); err != nil {
			t.Fatalf("finalize historical page %d: %v", pageIndex, err)
		}
		if result.Depleted {
			break
		}
		if result.NextCursor == "" {
			t.Fatalf("non-depleted page %d omitted its continuation cursor (status=%q)", pageIndex, result.ContinuationStatus)
		}
		request.Cursor = result.NextCursor
		request.RequestID = uuid.NewString()
		request.Now = request.Now.Add(time.Second)
	}
	if len(seen) != len(posts) {
		t.Fatalf("historical snapshot returned %d unique unvectorized posts, want %d: %v", len(seen), len(posts), seen)
	}
}

func TestHistoricalKeysetPlanUsesRecommendationIndexAtScaleIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "historical-plan-author")
	now := time.Now().UTC().Truncate(time.Second)
	createdAt := now.AddDate(0, 0, -400)
	if err := db.Exec(`
INSERT INTO posts (created_at, updated_at, author_id, content, visibility)
SELECT ?, ?, ?, 'historical-plan-' || row_num::text, 'public'
FROM generate_series(1, 30000) AS row_num`, createdAt, createdAt, author.ID).Error; err != nil {
		t.Fatalf("insert large historical fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Unscoped().Where("author_id = ? AND content LIKE ?", author.ID, "historical-plan-%").Delete(&models.Post{}).Error; err != nil {
			t.Errorf("delete historical query-plan fixture: %v", err)
		}
	})
	if err := db.Exec("ANALYZE posts").Error; err != nil {
		t.Fatalf("analyze historical fixture: %v", err)
	}
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := now.AddDate(0, 0, -7)
	page, err := repository.LoadHistoricalCandidates(context.Background(), CandidateQuery{
		Now: now, Range: CandidateRangeHistorical, RecentCutoff: cutoff, Limit: 200,
	})
	if err != nil || len(page.Candidates) != 200 || page.Exhausted || page.Cursor.PostID == 0 {
		t.Fatalf("large first historical batch size=%d exhausted=%t cursor=%+v err=%v", len(page.Candidates), page.Exhausted, page.Cursor, err)
	}
	planRows, err := db.Raw(`EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)
SELECT posts.id, posts.created_at
FROM posts
WHERE posts.deleted_at IS NULL
  AND posts.visibility = 'public'
  AND posts.reply_to_post_id IS NULL
  AND EXISTS (SELECT 1 FROM users AS post_author WHERE post_author.id = posts.author_id AND post_author.deleted_at IS NULL)
  AND posts.created_at < ?
  AND (posts.created_at, posts.id) < (?, ?)
  AND NOT EXISTS (SELECT 1 FROM user_post_reco_states AS rs WHERE rs.user_id = ? AND rs.post_id = posts.id AND rs.interacted = TRUE)
ORDER BY posts.created_at DESC, posts.id DESC
LIMIT 200`, cutoff, page.Cursor.CreatedAt, page.Cursor.PostID, uint(0)).Rows()
	if err != nil {
		t.Fatalf("explain historical keyset batch: %v", err)
	}
	defer planRows.Close()
	var plan strings.Builder
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			t.Fatalf("read historical query plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err := planRows.Err(); err != nil {
		t.Fatalf("read historical query plan: %v", err)
	}
	planText := plan.String()
	t.Logf("historical keyset plan:\n%s", planText)
	if !strings.Contains(planText, "idx_posts_recommendation_recent") || !strings.Contains(planText, "Index Scan") {
		t.Fatalf("large historical keyset plan did not use the recommendation created_at/id index:\n%s", planText)
	}
}

func addRecommendationCandidateMirrorPost(t *testing.T, db *gorm.DB, account models.DevDataMirrorAccount, post models.Post, ids *[]uint) {
	t.Helper()
	mapping := models.DevDataMirrorPost{
		Platform: "x", SourcePostID: "candidate-" + uuid.NewString(), SourceURL: "https://example.invalid/candidate",
		MirrorAccountID: account.ID, LocalPostID: post.ID, SourceCreatedAt: post.CreatedAt,
		ContentHash: embeddings.PostEmbeddingContentHash(post.Content), State: models.DevDataMirrorPostStateActive,
		ImportedAt: post.CreatedAt,
	}
	if err := db.Create(&mapping).Error; err != nil {
		t.Fatalf("create candidate mirror mapping: %v", err)
	}
	*ids = append(*ids, post.ID)
}

func equalCandidateIDs(left, right []uint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestRecommendationHydrationExcludesNonServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "pre-switch-viewer")
	author := newRecommendationCandidateIntegrationUser(t, db, "pre-switch-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	v1Only := newRecommendationCandidateIntegrationPost(t, db, author, "v1-only", now)
	v2Only := newRecommendationCandidateIntegrationPost(t, db, author, "v2-only", now.Add(-time.Minute))
	if err := db.Unscoped().Where("post_id = ? AND version = ?", v2Only.ID, "post_embedding_v1").Delete(&models.PostEmbedding{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostEmbedding{
		PostID: v2Only.ID, Version: "post_embedding_v2", Model: "candidate-v2", Dimensions: 2,
		Embedding: pgvector.NewVector([]float32{1, 0}), ContentHash: embeddings.PostEmbeddingContentHash(v2Only.Content),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserFollow{FollowerID: viewer.ID, FollowingID: author.ID}).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{v1Only.ID, v2Only.ID}
	userIDs := []uint{viewer.ID, author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	servingVersion := "post_embedding_v1"
	cfg := testDefaultRecommendationConfig()
	profile := Profile{PositiveVector: []float32{1, 0}}
	served := map[uint]ServedItem{}
	authenticated, err := loadRecommendationSourceCandidates(db, servingVersion, viewer.ID, profile, served, now, cfg, false, "posts.created_at DESC, posts.id DESC", 10, "recent")
	if err != nil {
		t.Fatal(err)
	}
	public, err := loadPublicRecommendationSourceCandidates(db, servingVersion, now, cfg, "posts.created_at DESC, posts.id DESC", 10, "recent", nil)
	if err != nil {
		t.Fatal(err)
	}
	semantic, err := loadRecommendationSemanticCandidates(db, servingVersion, viewer.ID, profile, served, now, testDefaultRecommendationConfig(), false, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertNonSemanticIncludesBoth := func(source string, candidates []Candidate) {
		t.Helper()
		if !equalCandidateIDs(candidateIDs(candidates), []uint{v1Only.ID, v2Only.ID}) {
			t.Fatalf("%s candidates=%v, want both posts independent of serving version", source, candidateIDs(candidates))
		}
	}
	assertNonSemanticIncludesBoth("authenticated recent", authenticated)
	assertNonSemanticIncludesBoth("public recent", public)
	hydrated, err := hydrateRecommendationCandidates(db, servingVersion, authenticated, now)
	if err != nil {
		t.Fatal(err)
	}
	hydratedFixtures := make(map[uint]RankedCandidate)
	for _, candidate := range hydrated {
		if candidate.Post.ID == v1Only.ID || candidate.Post.ID == v2Only.ID {
			hydratedFixtures[candidate.Post.ID] = candidate
		}
	}
	if len(hydratedFixtures) != 2 {
		t.Fatalf("hydrated fixture posts=%v, want both candidates", hydratedFixtures)
	}
	if len(hydratedFixtures[v1Only.ID].Embedding) == 0 {
		t.Fatalf("v1-only post %d has no serving-v1 embedding", v1Only.ID)
	}
	if hydratedFixtures[v2Only.ID].Embedding != nil {
		t.Fatalf("v2-only post %d unexpectedly fell back to another version: %v", v2Only.ID, hydratedFixtures[v2Only.ID].Embedding)
	}
	semanticFixtureCandidates := make(map[uint]Candidate)
	for _, candidate := range semantic {
		if candidate.PostID == v1Only.ID || candidate.PostID == v2Only.ID {
			semanticFixtureCandidates[candidate.PostID] = candidate
		}
	}
	if len(semanticFixtureCandidates) != 1 || !semanticFixtureCandidates[v1Only.ID].FromSemantic {
		t.Fatalf("semantic fixture candidates=%v, want only serving-v1 post %d", candidateIDs(semantic), v1Only.ID)
	}
}

func TestLoadPublicRecommendationCandidateSetUsesOnlyEligiblePublicRootsIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "public-author")
	now := time.Now().UTC()
	recentRoot := newRecommendationCandidateIntegrationPost(t, db, author, "recent-root", now.Add(-10*time.Minute))
	trendingRoot := newRecommendationCandidateIntegrationPost(t, db, author, "trending-root", now.Add(-30*time.Minute))
	if err := db.Model(&models.Post{}).Where("id = ?", trendingRoot.ID).Update("like_count", 5).Error; err != nil {
		t.Fatal(err)
	}

	deletedRoot := newRecommendationCandidateIntegrationPost(t, db, author, "deleted-root", now.Add(-5*time.Minute))
	conversationID := recentRoot.ID
	replyRootID := recentRoot.ID
	reply := models.Post{
		Model:          gorm.Model{CreatedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
		AuthorID:       author.ID,
		Content:        "reply-root",
		Visibility:     "public",
		ConversationID: &conversationID,
		ReplyToPostID:  &replyRootID,
	}
	if err := db.Delete(&deletedRoot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&reply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs := []uint{recentRoot.ID, trendingRoot.ID, deletedRoot.ID, reply.ID}
	t.Cleanup(func() {
		db.Unscoped().Where("id IN ?", postIDs).Delete(&models.Post{})
	})

	cfg := testDefaultRecommendationConfig()
	cfg.Candidates.ColdStart.Recent = 100
	cfg.Candidates.ColdStart.Trending = 100
	cfg.Candidates.ColdStart.Merged = 200

	candidateSet, err := servicePublicCandidateSetFromDB(db, now, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[uint]Candidate, len(candidateSet.Candidates))
	for _, candidate := range candidateSet.Candidates {
		byID[candidate.PostID] = candidate
	}
	for _, want := range []uint{recentRoot.ID, trendingRoot.ID} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("eligible public root %d missing from candidates=%v", want, candidateIDs(candidateSet.Candidates))
		}
	}
	for _, excluded := range []uint{deletedRoot.ID, reply.ID} {
		if _, ok := byID[excluded]; ok {
			t.Fatalf("ineligible post %d was returned in candidates=%v", excluded, candidateIDs(candidateSet.Candidates))
		}
	}
	if !byID[recentRoot.ID].FromRecent {
		t.Fatalf("recent root candidate metadata=%#v", byID[recentRoot.ID])
	}
	if !byID[trendingRoot.ID].FromRecent || !byID[trendingRoot.ID].FromTrending {
		t.Fatalf("trending root candidate metadata=%#v", byID[trendingRoot.ID])
	}
	if candidateSet.FollowingCount != 0 {
		t.Fatalf("anonymous following count=%d, want 0", candidateSet.FollowingCount)
	}
	if candidateSet.SemanticCount != 0 {
		t.Fatalf("anonymous semantic count=%d, want 0", candidateSet.SemanticCount)
	}

	excludedSet, err := servicePublicCandidateSetFromDB(db, now, cfg, map[uint]struct{}{recentRoot.ID: {}})
	if err != nil {
		t.Fatal(err)
	}
	if containsRecommendationCandidateID(excludedSet.Candidates, recentRoot.ID) {
		t.Fatalf("explicitly excluded post %d was returned", recentRoot.ID)
	}
}

func candidateIDs(candidates []Candidate) []uint {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.PostID)
	}
	return ids
}

func containsRecommendationCandidateID(candidates []Candidate, want uint) bool {
	for _, candidate := range candidates {
		if candidate.PostID == want {
			return true
		}
	}
	return false
}

func TestRecommendationRecallSkipsDeletedAuthorBeforeLimitIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "viewer")
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationPost(t, db, deletedAuthor, "bad", now)
	goodArticle := newRecommendationCandidateIntegrationPost(t, db, validAuthor, "good", now.Add(-time.Minute))
	postIDs := []uint{badArticle.ID, goodArticle.ID}
	userIDs := []uint{viewer.ID, validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := loadRecommendationSourceCandidates(
		db,
		"post_embedding_v1",
		viewer.ID,
		Profile{},
		map[uint]ServedItem{},
		now,
		testDefaultRecommendationConfig(),
		false,
		"posts.created_at DESC, posts.id DESC",
		1,
		"recent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidate count=%d, want 1", len(candidates))
	}
	if candidates[0].PostID != goodArticle.ID {
		t.Fatalf("candidate article ID=%d, want valid article %d", candidates[0].PostID, goodArticle.ID)
	}
	if candidates[0].PostID == badArticle.ID {
		t.Fatal("deleted-author article was returned")
	}
}

func TestRecommendationHydrationDiscardsDeletedAuthorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationPost(t, db, validAuthor, "valid", now)
	badArticle := newRecommendationCandidateIntegrationPost(t, db, deletedAuthor, "bad", now)
	postIDs := []uint{validArticle.ID, badArticle.ID}
	userIDs := []uint{validAuthor.ID, deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	hydrated, err := hydrateRecommendationCandidates(
		db,
		"post_embedding_v1",
		[]Candidate{
			{PostID: validArticle.ID, FromRecent: true},
			{PostID: badArticle.ID, FromTrending: true},
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 1 {
		t.Fatalf("hydrated count=%d, want 1", len(hydrated))
	}
	if hydrated[0].Post.ID != validArticle.ID {
		t.Fatalf("hydrated article ID=%d, want %d", hydrated[0].Post.ID, validArticle.ID)
	}
	if hydrated[0].Post.Author.ID != validAuthor.ID || hydrated[0].Post.Author.ID != hydrated[0].Post.AuthorID {
		t.Fatalf("hydrated author=%#v author_id=%d", hydrated[0].Post.Author, hydrated[0].Post.AuthorID)
	}
	if len(hydrated[0].Embedding) != 2 || hydrated[0].Embedding[0] != 1 {
		t.Fatalf("hydrated embedding=%v, want the valid candidate embedding", hydrated[0].Embedding)
	}
}

func TestRecommendationHydrationAllInvalidAuthorsReturnsEmptyIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	deletedAuthor := newRecommendationCandidateIntegrationUser(t, db, "deleted-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	badArticle := newRecommendationCandidateIntegrationPost(t, db, deletedAuthor, "bad", now)
	postIDs := []uint{badArticle.ID}
	userIDs := []uint{deletedAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	if err := db.Delete(&deletedAuthor).Error; err != nil {
		t.Fatal(err)
	}

	hydrated, err := hydrateRecommendationCandidates(
		db,
		"post_embedding_v1",
		[]Candidate{{PostID: badArticle.ID}},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 0 {
		t.Fatalf("hydrated count=%d, want 0", len(hydrated))
	}
}

func TestRecommendationHydrationKeepsNonSemanticCandidateWithoutServingEmbeddingIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	author := newRecommendationCandidateIntegrationUser(t, db, "missing-serving-embedding")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	article := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, author, "missing-serving-embedding", now)
	postIDs := []uint{article.ID}
	userIDs := []uint{author.ID}
	t.Cleanup(func() { cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs) })

	hydrated, err := hydrateRecommendationCandidates(db, "post_embedding_v1", []Candidate{
		{PostID: article.ID},
		{PostID: article.ID, FromSemantic: true, PositiveSemanticSimilarity: .99},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) != 1 || hydrated[0].Post.ID != article.ID {
		t.Fatalf("hydrated=%+v, want the eligible non-semantic candidate only", hydrated)
	}
	if hydrated[0].Embedding != nil || hydrated[0].Candidate.FromSemantic {
		t.Fatalf("hydrated candidate=%+v, want nil embedding and no semantic provenance", hydrated[0])
	}
	semanticOnly, err := hydrateRecommendationCandidates(db, "post_embedding_v1", []Candidate{
		{PostID: article.ID, FromSemantic: true, PositiveSemanticSimilarity: .99},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(semanticOnly) != 0 {
		t.Fatalf("late semantic candidate=%+v, want dropped after its serving vector disappears", semanticOnly)
	}
}

func TestRecommendationHydrationPropagatesEmbeddingErrorIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	validAuthor := newRecommendationCandidateIntegrationUser(t, db, "valid-author")
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	validArticle := newRecommendationCandidateIntegrationPost(t, db, validAuthor, "valid", now)
	postIDs := []uint{validArticle.ID}
	userIDs := []uint{validAuthor.ID}
	t.Cleanup(func() {
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := hydrateRecommendationCandidates(
		db.WithContext(canceled),
		"post_embedding_v1",
		[]Candidate{{PostID: validArticle.ID}},
		now,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
}

package recommendation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"Go.exchange/config"
	"Go.exchange/embeddings"
	"Go.exchange/eventing"
	"Go.exchange/models"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

func TestOnboardingInterestRecentRecallUsesUserChoicesAndExistingEligibilityIntegration(t *testing.T) {
	db := openRecommendationCandidateIntegrationDB(t)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	viewer := newRecommendationCandidateIntegrationUser(t, db, "interest-viewer")
	otherViewer := newRecommendationCandidateIntegrationUser(t, db, "interest-other-viewer")
	aiAuthor := newRecommendationCandidateIntegrationUser(t, db, "interest-ai-author")
	spaceAuthor := newRecommendationCandidateIntegrationUser(t, db, "interest-space-author")
	mutedAuthor := newRecommendationCandidateIntegrationUser(t, db, "interest-muted-author")
	var postIDs []uint
	var accountIDs []uint
	userIDs := []uint{viewer.ID, otherViewer.ID, aiAuthor.ID, spaceAuthor.ID, mutedAuthor.ID}
	// Register cleanup before the first source mapping or test preference is written.
	t.Cleanup(func() {
		if err := db.Unscoped().Where("user_id IN ?", userIDs).Delete(&models.UserOnboardingInterest{}).Error; err != nil {
			t.Errorf("delete onboarding interest fixtures: %v", err)
		}
		if err := db.Unscoped().Where("local_post_id IN ?", postIDs).Delete(&models.DevDataMirrorPost{}).Error; err != nil {
			t.Errorf("delete onboarding mirror-post fixtures: %v", err)
		}
		if len(postIDs) > 0 {
			if err := db.Unscoped().Where("reply_to_post_id IN ?", postIDs).Delete(&models.Post{}).Error; err != nil {
				t.Errorf("delete onboarding reply fixtures: %v", err)
			}
		}
		if err := db.Unscoped().Where("id IN ?", accountIDs).Delete(&models.DevDataMirrorAccount{}).Error; err != nil {
			t.Errorf("delete onboarding mirror-account fixtures: %v", err)
		}
		cleanupRecommendationCandidateIntegrationData(db, postIDs, userIDs)
	})
	aiRegistryKey := "ai-source-" + uuid.NewString()
	mutedRegistryKey := "ai-muted-source-" + uuid.NewString()
	spaceRegistryKey := "space-source-" + uuid.NewString()
	aiAccount := createOnboardingInterestMirrorAccount(t, db, aiAuthor, aiRegistryKey)
	accountIDs = append(accountIDs, aiAccount.ID)
	spaceAccount := createOnboardingInterestMirrorAccount(t, db, spaceAuthor, spaceRegistryKey)
	accountIDs = append(accountIDs, spaceAccount.ID)
	mutedAccount := createOnboardingInterestMirrorAccount(t, db, mutedAuthor, mutedRegistryKey)
	accountIDs = append(accountIDs, mutedAccount.ID)

	aiValid := createOnboardingInterestMappedPost(t, db, aiAccount, aiAuthor, "ai-valid", now.Add(-6*time.Hour), &postIDs)
	aiMuted := createOnboardingInterestMappedPost(t, db, mutedAccount, mutedAuthor, "ai-muted", now.Add(-5*time.Hour), &postIDs)
	aiNotInterested := createOnboardingInterestMappedPost(t, db, aiAccount, aiAuthor, "ai-not-interested", now.Add(-4*time.Hour), &postIDs)
	aiInteracted := createOnboardingInterestMappedPost(t, db, aiAccount, aiAuthor, "ai-interacted", now.Add(-3*time.Hour), &postIDs)
	aiHardServed := createOnboardingInterestMappedPost(t, db, aiAccount, aiAuthor, "ai-hard-served", now.Add(-2*time.Hour), &postIDs)
	aiSoftServed := createOnboardingInterestMappedPost(t, db, aiAccount, aiAuthor, "ai-soft-served", now.Add(-90*time.Minute), &postIDs)
	aiReplyRoot := models.Post{
		Model:    gorm.Model{CreatedAt: now.Add(-80 * time.Minute), UpdatedAt: now.Add(-80 * time.Minute)},
		AuthorID: aiAuthor.ID, Content: "reply root", Visibility: "public",
	}
	if err := db.Create(&aiReplyRoot).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, aiReplyRoot.ID)
	rootID := aiReplyRoot.ID
	aiReply := models.Post{
		Model:    gorm.Model{CreatedAt: now.Add(-70 * time.Minute), UpdatedAt: now.Add(-70 * time.Minute)},
		AuthorID: aiAuthor.ID, Content: "mapped reply", Visibility: "public",
		ReplyToPostID: &rootID, ConversationID: &rootID,
	}
	if err := db.Create(&aiReply).Error; err != nil {
		t.Fatal(err)
	}
	postIDs = append(postIDs, aiReply.ID)
	aiReplyMap := models.DevDataMirrorPost{
		Platform: "x", SourcePostID: "ai-reply-" + uuid.NewString(), SourceURL: "https://example.invalid/ai-reply",
		MirrorAccountID: aiAccount.ID, LocalPostID: aiReply.ID, SourceCreatedAt: aiReply.CreatedAt,
		ContentHash: "reply", State: models.DevDataMirrorPostStateActive, ImportedAt: now,
	}
	if err := db.Create(&aiReplyMap).Error; err != nil {
		t.Fatal(err)
	}
	aiDeleted := createOnboardingInterestMappedPost(t, db, aiAccount, aiAuthor, "ai-deleted", now.Add(-50*time.Minute), &postIDs)
	if err := db.Delete(&aiDeleted).Error; err != nil {
		t.Fatal(err)
	}
	spaceValid := createOnboardingInterestMappedPost(t, db, spaceAccount, spaceAuthor, "space-valid", now.Add(-time.Hour), &postIDs)
	neutral := newRecommendationCandidateIntegrationPostWithoutEmbedding(t, db, spaceAuthor, "neutral-recent", now.Add(-30*time.Minute))
	addOnboardingCandidateEmbedding(t, db, neutral)
	postIDs = append(postIDs, neutral.ID)
	if err := db.Create(&models.UserOnboardingInterest{UserID: viewer.ID, TopicSlug: "ai"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserOnboardingInterest{UserID: otherViewer.ID, TopicSlug: "space"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.UserMute{MuterID: viewer.ID, MutedID: mutedAuthor.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.PostBehavior{
		UserID: viewer.ID, PostID: aiNotInterested.ID, Action: eventing.RecommendationBehaviorActionNotInterested,
		Active: true, LastSeenAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := config.LoadCuratedTopics()
	if err != nil {
		t.Fatal(err)
	}
	topicSources := make(map[string][]string, len(catalog.Topics))
	allSlugs := make([]string, 0, len(catalog.Topics))
	for _, topic := range catalog.Topics {
		if topic.Enabled {
			topicSources[topic.Slug] = topic.SourceKeys
			allSlugs = append(allSlugs, topic.Slug)
		}
	}
	// Use unique source keys for this disposable fixture so it remains isolated
	// from real mirror registry rows while exercising the selected topic mapping.
	topicSources["ai"] = []string{aiRegistryKey, mutedRegistryKey}
	topicSources["space"] = []string{spaceRegistryKey}
	aiSlugs, err := repository.LoadOnboardingInterestSlugs(context.Background(), viewer.ID, allSlugs)
	if err != nil {
		t.Fatal(err)
	}
	spaceSlugs, err := repository.LoadOnboardingInterestSlugs(context.Background(), otherViewer.ID, allSlugs)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(aiSlugs, []string{"ai"}) || !equalStrings(spaceSlugs, []string{"space"}) {
		t.Fatalf("interest query did not isolate users: AI=%v Space=%v", aiSlugs, spaceSlugs)
	}
	cfg := testDefaultRecommendationConfig()
	base := candidateQueryForTest("post_embedding_v1", 0, Profile{}, ServedHistory{
		aiHardServed.ID: {Hard: true, LastServedAt: now.Add(-time.Minute)},
		aiSoftServed.ID: {Soft: true, LastServedAt: now.Add(-time.Hour)},
	}, now, cfg, false, 4)
	aiQuery := base
	aiQuery.UserID = viewer.ID
	aiQuery.OnboardingInterestSourceKeys = onboardingSourceKeys(aiSlugs, topicSources)
	aiQuery.OnboardingInterestLimit = onboardingInterestCandidateBudget(aiQuery.Limit)
	aiQuery.InteractedPostIDs = map[uint]struct{}{aiInteracted.ID: {}}
	aiCandidates, err := repository.LoadRecentCandidates(context.Background(), aiQuery)
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateOrder := func(label string, candidates []Candidate, want []uint) {
		t.Helper()
		got := make([]uint, 0, len(candidates))
		for _, candidate := range candidates {
			got = append(got, candidate.PostID)
		}
		if len(got) != len(want) {
			t.Fatalf("%s candidate IDs=%v, want %v", label, got, want)
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("%s candidate IDs=%v, want %v", label, got, want)
			}
		}
	}
	assertCandidateOrder("AI recent", aiCandidates, []uint{aiValid.ID, neutral.ID, spaceValid.ID})
	if !aiCandidates[0].FromOnboardingInterest {
		t.Fatalf("AI recent candidates=%#v, want onboarding-interest Post %d first", aiCandidates, aiValid.ID)
	}
	assertUniqueRecommendationPostIDs(t, aiCandidates)
	for _, candidate := range aiCandidates {
		if candidate.PostID == aiMuted.ID || candidate.PostID == aiNotInterested.ID || candidate.PostID == aiInteracted.ID ||
			candidate.PostID == aiHardServed.ID || candidate.PostID == aiReply.ID || candidate.PostID == aiDeleted.ID {
			t.Fatalf("interest branch bypassed existing eligibility for post %d: %#v", candidate.PostID, aiCandidates)
		}
	}
	if got := countOnboardingInterestCandidates(aiCandidates); got != 1 {
		t.Fatalf("AI candidate count=%d want=1", got)
	}

	spaceQuery := base
	spaceQuery.UserID = otherViewer.ID
	spaceQuery.OnboardingInterestSourceKeys = onboardingSourceKeys(spaceSlugs, topicSources)
	spaceQuery.OnboardingInterestLimit = onboardingInterestCandidateBudget(spaceQuery.Limit)
	spaceCandidates, err := repository.LoadRecentCandidates(context.Background(), spaceQuery)
	if err != nil {
		t.Fatal(err)
	}
	assertCandidateOrder("Space recent", spaceCandidates, []uint{spaceValid.ID, neutral.ID, aiInteracted.ID, aiNotInterested.ID})
	if !spaceCandidates[0].FromOnboardingInterest {
		t.Fatalf("Space recent candidates=%#v, want onboarding-interest Post %d first", spaceCandidates, spaceValid.ID)
	}
	assertUniqueRecommendationPostIDs(t, spaceCandidates)
	if spaceCandidates[0].PostID == aiValid.ID {
		t.Fatal("Space interest returned the AI candidate")
	}

	softQuery := aiQuery
	softQuery.SoftOnly = true
	softQuery.Served = ServedHistory{aiSoftServed.ID: {Soft: true, LastServedAt: now.Add(-time.Hour)}}
	softCandidates, err := repository.LoadRecentCandidates(context.Background(), softQuery)
	if err != nil {
		t.Fatal(err)
	}
	if len(softCandidates) != 1 || softCandidates[0].PostID != aiSoftServed.ID || !softCandidates[0].FromOnboardingInterest {
		t.Fatalf("soft interest candidates=%#v, want only eligible soft-served post", softCandidates)
	}

	// Execute the exact bounded interest query with PostgreSQL's runtime planner.
	planQuery := repository.onboardingInterestCandidateQuery(db.Session(&gorm.Session{DryRun: true}), aiQuery).
		Order("posts.created_at DESC, posts.id DESC").Limit(aiQuery.OnboardingInterestLimit).Find(&[]uint{})
	if planQuery.Error != nil {
		t.Fatal(planQuery.Error)
	}
	planSQL := db.Dialector.Explain(planQuery.Statement.SQL.String(), planQuery.Statement.Vars...)
	var planJSON string
	if err := db.Raw("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + planSQL).Row().Scan(&planJSON); err != nil {
		t.Fatalf("EXPLAIN onboarding-interest candidate query: %v", err)
	}
	var decoded any
	if err := json.Unmarshal([]byte(planJSON), &decoded); err != nil {
		t.Fatalf("decode EXPLAIN JSON: %v", err)
	}
	t.Logf("onboarding-interest EXPLAIN (ANALYZE, BUFFERS): %s", fmt.Sprint(decoded))
}

func createOnboardingInterestMirrorAccount(t *testing.T, db *gorm.DB, user models.User, registryKey string) models.DevDataMirrorAccount {
	t.Helper()
	account := models.DevDataMirrorAccount{
		RegistryKey: registryKey, Platform: "x", SourceUserID: uuid.NewString(), SourceHandle: registryKey,
		LocalUserID: user.ID, Category: "technology_ai", Enabled: true,
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	return account
}

func createOnboardingInterestMappedPost(t *testing.T, db *gorm.DB, account models.DevDataMirrorAccount, author models.User, label string, createdAt time.Time, trackedPostIDs *[]uint) models.Post {
	t.Helper()
	post := models.Post{
		Model:    gorm.Model{CreatedAt: createdAt, UpdatedAt: createdAt},
		AuthorID: author.ID, Content: label, Visibility: "public",
	}
	if err := db.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	*trackedPostIDs = append(*trackedPostIDs, post.ID)
	addOnboardingCandidateEmbedding(t, db, post)
	mapping := models.DevDataMirrorPost{
		Platform: "x", SourcePostID: label + "-" + uuid.NewString(), SourceURL: "https://example.invalid/" + label,
		MirrorAccountID: account.ID, LocalPostID: post.ID, SourceCreatedAt: createdAt,
		ContentHash: label, State: models.DevDataMirrorPostStateActive, ImportedAt: createdAt,
	}
	if err := db.Create(&mapping).Error; err != nil {
		t.Fatal(err)
	}
	return post
}

func addOnboardingCandidateEmbedding(t *testing.T, db *gorm.DB, post models.Post) {
	t.Helper()
	embedding := models.PostEmbedding{
		PostID: post.ID, Version: "post_embedding_v1", Model: "onboarding-interest-test",
		Dimensions: 2, Embedding: pgvector.NewVector([]float32{1, 0}),
		ContentHash: embeddings.PostEmbeddingContentHash(post.Content),
	}
	if err := db.Create(&embedding).Error; err != nil {
		t.Fatalf("create serving embedding for onboarding post %d: %v", post.ID, err)
	}
}

func countOnboardingInterestCandidates(candidates []Candidate) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.FromOnboardingInterest {
			count++
		}
	}
	return count
}

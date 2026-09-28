package recommendation

import (
	"context"
	"testing"
	"time"

	"Go.exchange/config"
	"gorm.io/gorm"
)

func recommendationDBTestContext(db *gorm.DB) context.Context {
	if db != nil && db.Statement != nil && db.Statement.Context != nil {
		return db.Statement.Context
	}
	return context.Background()
}

func newIntegrationCandidateRepository(db *gorm.DB) (*GormCandidateRepository, error) {
	return NewGormCandidateRepository(db)
}

func testDefaultRecommendationConfig() config.RecommendationConfig {
	cfg := serviceTestConfig()
	cfg.ServingTimeoutMS = 5000
	cfg.BehaviorWeights = config.RecommendationBehaviorWeights{
		View: 0.25, Like: 4, Click: 1, QualifiedRead: 2.5, Reply: 5, QuickBounce: -2, NotInterested: -8,
	}
	cfg.SemanticRecall = config.RecommendationSemanticRecallConfig{RecentWindowDays: 7, RecentRatio: 0.85}
	cfg.Trending = config.RecommendationTrendingConfig{MaxAgeDays: 3, HalfLifeHours: 12, ReplyFactor: 1.5}
	cfg.Exploration = config.RecommendationExplorationConfig{Ratio: 0.10, MaxSlots: 3, RecentWindowDays: 7, NovelPostMaxAgeDays: 30}
	cfg.LanguageAffinity = config.RecommendationLanguageAffinityConfig{Enabled: true, Weight: 0.35, EvidenceSaturationScale: 5, MaxBehaviorShare: 0.95}
	cfg.SignalHalfLifeDays = 14
	cfg.FeedbackLookbackDays = 90
	cfg.PositiveSignalCoexistBonus = 1
	cfg.PositivePostWeightCap = 7
	cfg.SemanticWeight = 4
	cfg.NegativeSemanticWeight = 1.5
	cfg.NegativeConfidenceSaturationScale = 12
	cfg.TrendingWeight = 0.5
	cfg.AuthorAffinityWeight = 1
	cfg.AuthorAffinitySaturationScale = 6
	cfg.FollowingBonus = 0.5
	cfg.OutOfNetworkMinRatio = 0.30
	cfg.ServedHardExclusionMinutes = 30
	cfg.ServedSoftLookbackDays = 7
	cfg.ServedHistoryLimit = 1000
	cfg.GuestServedHistoryLimit = 2000
	cfg.GuestServedHistoryTTLHours = 24
	cfg.Diversity = config.RecommendationDiversityConfig{
		Enabled: true, AuthorWindowSize: 8, MaxSameAuthorInWindow: 2,
		SemanticDuplicateThreshold: 0.92, SemanticDuplicatePenalty: 1,
	}
	cfg.Trace = config.RecommendationTraceConfig{
		PersistTimeoutMS: defaultTracePersistTimeoutMS, QueueCapacity: defaultTraceQueueCapacity,
		WorkerCount: defaultTraceWorkerCount, ShutdownDrainTimeoutMS: defaultTraceShutdownDrainTimeoutMS,
		ResultRetentionDays:           config.DefaultRecommendationTraceResultRetentionDays,
		RequestRetentionDays:          config.DefaultRecommendationTraceRequestRetentionDays,
		CleanupIntervalSeconds:        config.DefaultRecommendationTraceCleanupIntervalSeconds,
		CleanupCatchupIntervalSeconds: config.DefaultRecommendationTraceCleanupCatchupIntervalSeconds,
		CleanupResultBatchSize:        config.DefaultRecommendationTraceCleanupResultBatchSize,
		CleanupRequestBatchSize:       config.DefaultRecommendationTraceCleanupRequestBatchSize,
		CleanupRunBudgetSeconds:       config.DefaultRecommendationTraceCleanupRunBudgetSeconds,
		CleanupMaxResultRowsPerRun:    config.DefaultRecommendationTraceCleanupMaxResultRowsPerRun,
		CleanupMaxRequestRowsPerRun:   config.DefaultRecommendationTraceCleanupMaxRequestRowsPerRun,
	}
	cfg.Candidates = config.RecommendationCandidatesConfig{
		Personalized: config.RecommendationCandidateCaps{Semantic: 200, Following: 150, Recent: 150, Trending: 150, Merged: 500},
		ColdStart:    config.RecommendationCandidateCaps{Following: 200, Recent: 200, Trending: 200, Merged: 500},
	}
	return cfg
}

func TestDefaultRecommendationConfigIncludesTraceDispatcherDefaults(t *testing.T) {
	trace := testDefaultRecommendationConfig().Trace
	if trace.PersistTimeoutMS != defaultTracePersistTimeoutMS ||
		trace.QueueCapacity != defaultTraceQueueCapacity ||
		trace.WorkerCount != defaultTraceWorkerCount ||
		trace.ShutdownDrainTimeoutMS != defaultTraceShutdownDrainTimeoutMS ||
		trace.ShutdownDrainTimeoutMS <= 0 {
		t.Fatalf("integration test trace config=%+v, want dispatcher defaults with a positive shutdown timeout", trace)
	}
}

func candidateQueryForTest(servingVersion string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) CandidateQuery {
	return CandidateQuery{
		UserID: userID, ServingVersion: servingVersion, Now: now, Limit: limit,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: cfg.SemanticRecall.RecentRatio,
		SemanticRecentCutoff: now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays),
		TrendingCutoff:       now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor:  cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		Served: served, SoftOnly: softOnly, MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs: profile.InteractedPostIDs,
	}
}

func serviceCandidateSetFromDB(db *gorm.DB, version string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool) (CandidateSetSummary, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	return buildCandidateSet(recommendationDBTestContext(db), repository, version, userID, profile, served, now, cfg, softOnly)
}

func servicePublicCandidateSetFromDB(db *gorm.DB, now time.Time, cfg config.RecommendationConfig, excluded map[uint]struct{}) (CandidateSetSummary, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	return buildPublicCandidateSet(recommendationDBTestContext(db), repository, now, cfg, excluded)
}

func loadRecommendationSemanticCandidates(db *gorm.DB, version string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadSemanticCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadRecommendationSemanticPool(db *gorm.DB, version string, userID uint, profile Profile, served ServedHistory, now time.Time, softOnly bool, cutoff time.Time, comparison string, limit int, excluded map[uint]struct{}) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	query := CandidateQuery{
		UserID: userID, ServingVersion: version, Now: now, Limit: limit,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: 0, SemanticRecentCutoff: cutoff,
		Served: served, SoftOnly: softOnly, MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs: profile.InteractedPostIDs,
	}
	_ = comparison
	candidates, err := repository.LoadSemanticCandidates(recommendationDBTestContext(db), query)
	if err != nil {
		return nil, err
	}
	if len(excluded) == 0 {
		return candidates, nil
	}
	filtered := make([]Candidate, 0, len(candidates))
	for _, item := range candidates {
		if _, excluded := excluded[item.PostID]; !excluded {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func loadRecommendationFollowingCandidates(db *gorm.DB, version string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadFollowingCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadRecommendationSourceCandidates(db *gorm.DB, version string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool, _ interface{}, limit int, _ string) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadRecentCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadRecommendationTrendingCandidates(db *gorm.DB, version string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool, limit int) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadTrendingCandidates(recommendationDBTestContext(db), candidateQueryForTest(version, userID, profile, served, now, cfg, softOnly, limit))
}

func loadPublicRecommendationSourceCandidates(db *gorm.DB, _ string, now time.Time, _ config.RecommendationConfig, _ interface{}, limit int, _ string, excluded map[uint]struct{}) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadPublicRecentCandidates(recommendationDBTestContext(db), PublicCandidateQuery{Now: now, Limit: limit, ExcludedPostIDs: excluded})
}

func loadPublicRecommendationTrendingCandidates(db *gorm.DB, _ string, now time.Time, cfg config.RecommendationConfig, limit int, excluded map[uint]struct{}) ([]Candidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.LoadPublicTrendingCandidates(recommendationDBTestContext(db), PublicCandidateQuery{
		Now: now, Limit: limit, TrendingCutoff: now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor: cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		ExcludedPostIDs: excluded,
	})
}

func hydrateRecommendationCandidates(db *gorm.DB, version string, candidates []Candidate, now time.Time) ([]RankedCandidate, error) {
	repository, err := NewGormCandidateRepository(db)
	if err != nil {
		return nil, err
	}
	return repository.HydrateCandidates(recommendationDBTestContext(db), version, candidates, now)
}

func loadMaterializedProfileForIntegration(db *gorm.DB, userID uint, version string, now time.Time, cfg config.RecommendationConfig) (Profile, error) {
	repository, err := NewGormProfileRepository(db)
	if err != nil {
		return Profile{}, err
	}
	loaded, err := repository.Load(recommendationDBTestContext(db), ProfileLoadQuery{
		UserID: userID, EmbeddingVersion: version,
		ExpectedProfileVersion: MaterializedProfileVersion, ExpectedProfileConfigHash: ProfileConfigHash(cfg, version),
		Now: now, NegativeConfidenceHalfLifeDays: cfg.SignalHalfLifeDays,
		NegativeConfidenceSaturation: cfg.NegativeConfidenceSaturationScale,
	})
	return loaded.Profile, err
}

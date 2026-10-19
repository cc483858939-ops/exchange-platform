package recommendation

import (
	"context"
	"errors"
	"sort"
	"time"

	"Go.exchange/config"
	"go.opentelemetry.io/otel/attribute"
)

const (
	onboardingInterestSignalLimit = 8
	onboardingInterestMaxBoost    = 0.4
	onboardingInterestMaxBudget   = 40
)

type onboardingInterestContext struct {
	sourceKeys []string
	strength   float64
}

func onboardingInterestStrength(signalCount int) float64 {
	if signalCount >= onboardingInterestSignalLimit {
		return 0
	}
	if signalCount <= 0 {
		return 1
	}
	return 1 - float64(signalCount)/onboardingInterestSignalLimit
}

func onboardingSourceKeys(slugs []string, topicSources map[string][]string) []string {
	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for _, slug := range slugs {
		for _, key := range topicSources[slug] {
			if key == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func enabledOnboardingTopicSlugs(topicSources map[string][]string) []string {
	slugs := make([]string, 0, len(topicSources))
	for slug := range topicSources {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

func onboardingInterestCandidateBudget(recentLimit int) int {
	if recentLimit <= 0 {
		return 0
	}
	budget := recentLimit / 4
	if budget > onboardingInterestMaxBudget {
		budget = onboardingInterestMaxBudget
	}
	return budget
}

func recommendationCandidateQuery(servingVersion string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool) CandidateQuery {
	windowStart, windowEnd := config.RecommendationCandidateWindowBounds(now, cfg.CandidateWindowDays)
	return CandidateQuery{
		UserID: userID, ServingVersion: servingVersion, Now: now,
		CandidateWindowStart: windowStart, CandidateWindowEnd: windowEnd,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: cfg.SemanticRecall.RecentRatio,
		SemanticRecentCutoff: now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays),
		TrendingCutoff:       now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor:  cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		Served: served, SoftOnly: softOnly,
		MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs:             profile.InteractedPostIDs,
	}
}

func buildCandidateSet(ctx context.Context, repository CandidateRepository, servingVersion string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool, onboarding ...onboardingInterestContext) (CandidateSetSummary, error) {
	phase := phaseName(softOnly)
	ctx, span := startRecommendationSpan(ctx, "recommendation.recall", attribute.String("recommendation.phase", phase))
	defer span.End()
	if repository == nil {
		err := errors.New("recommendation candidate repository is nil")
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	caps := candidateCaps(profile, cfg)
	query := recommendationCandidateQuery(servingVersion, userID, profile, served, now, cfg, softOnly)
	interest := onboardingInterestContext{}
	if len(onboarding) > 0 {
		interest = onboarding[0]
	}
	if interest.strength > 0 {
		query.OnboardingInterestLimit = onboardingInterestCandidateBudget(caps.Recent)
		if query.OnboardingInterestLimit > 0 {
			query.OnboardingInterestSourceKeys = append([]string(nil), interest.sourceKeys...)
		}
	}
	query.Limit = caps.Semantic
	semantic, err := loadRecommendationCandidates(ctx, "recommendation.recall.semantic", "semantic", phase, func(stageCtx context.Context) ([]Candidate, error) {
		return repository.LoadSemanticCandidates(stageCtx, query)
	})
	if err != nil {
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Following
	following, err := loadRecommendationCandidates(ctx, "recommendation.recall.following", "following", phase, func(stageCtx context.Context) ([]Candidate, error) {
		return repository.LoadFollowingCandidates(stageCtx, query)
	})
	if err != nil {
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Recent
	recent, err := loadRecommendationCandidates(ctx, "recommendation.recall.recent", "recent", phase, func(stageCtx context.Context) ([]Candidate, error) {
		return repository.LoadRecentCandidates(stageCtx, query)
	})
	if err != nil {
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Trending
	trending, err := loadRecommendationCandidates(ctx, "recommendation.recall.trending", "trending", phase, func(stageCtx context.Context) ([]Candidate, error) {
		return repository.LoadTrendingCandidates(stageCtx, query)
	})
	if err != nil {
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	_, fusionSpan := startRecommendationSpan(ctx, "recommendation.recall.fusion",
		attribute.String("recommendation.source", "fused"),
		attribute.String("recommendation.phase", phase),
	)
	merged := FuseCandidates(caps.Merged, fusionConfig(cfg),
		CandidateSet{Source: CandidateSourceSemantic, Candidates: semantic},
		CandidateSet{Source: CandidateSourceFollowing, Candidates: following},
		CandidateSet{Source: CandidateSourceRecent, Candidates: recent},
		CandidateSet{Source: CandidateSourceTrending, Candidates: trending},
	)
	fusionSpan.SetAttributes(attribute.Int("recommendation.candidate_count", len(merged)))
	fusionSpan.End()
	for index := range merged {
		if item, ok := served[merged[index].PostID]; ok {
			merged[index].LastServedAt = item.LastServedAt
			merged[index].WasSoftServed = softOnly && item.Soft && !item.Hard
		}
	}
	interestCandidateCount := 0
	for _, candidate := range recent {
		if candidate.FromOnboardingInterest {
			interestCandidateCount++
		}
	}
	span.SetAttributes(attribute.Int("recommendation.candidate_count", len(merged)))
	return CandidateSetSummary{
		Candidates: merged, SemanticCount: len(semantic), FollowingCount: len(following),
		RecentCount: len(recent), RecentPostIDs: CandidatePostIDs(recent), TrendingCount: len(trending),
		OnboardingInterestActive:         len(query.OnboardingInterestSourceKeys) > 0,
		OnboardingInterestCandidateCount: interestCandidateCount,
	}, nil
}

func buildPublicCandidateSet(ctx context.Context, repository CandidateRepository, servingVersion string, now time.Time, cfg config.RecommendationConfig, excluded map[uint]struct{}, phase string) (CandidateSetSummary, error) {
	ctx, span := startRecommendationSpan(ctx, "recommendation.recall", attribute.String("recommendation.phase", phase))
	defer span.End()
	if repository == nil {
		err := errors.New("recommendation candidate repository is nil")
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	caps := cfg.Candidates.ColdStart
	windowStart, windowEnd := config.RecommendationCandidateWindowBounds(now, cfg.CandidateWindowDays)
	query := PublicCandidateQuery{
		ServingVersion: servingVersion, Now: now, CandidateWindowStart: windowStart, CandidateWindowEnd: windowEnd,
		TrendingCutoff:      now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor: cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		ExcludedPostIDs: excluded,
	}
	query.Limit = caps.Recent
	recent, err := loadRecommendationCandidates(ctx, "recommendation.recall.recent", "public_recent", phase, func(stageCtx context.Context) ([]Candidate, error) {
		return repository.LoadPublicRecentCandidates(stageCtx, query)
	})
	if err != nil {
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Trending
	trending, err := loadRecommendationCandidates(ctx, "recommendation.recall.trending", "public_trending", phase, func(stageCtx context.Context) ([]Candidate, error) {
		return repository.LoadPublicTrendingCandidates(stageCtx, query)
	})
	if err != nil {
		finishRecommendationSpan(span, err)
		return CandidateSetSummary{}, err
	}
	_, fusionSpan := startRecommendationSpan(ctx, "recommendation.recall.fusion",
		attribute.String("recommendation.source", "fused"),
		attribute.String("recommendation.phase", phase),
	)
	merged := FuseCandidates(
		caps.Merged, fusionConfig(cfg),
		CandidateSet{Source: CandidateSourceRecent, Candidates: recent},
		CandidateSet{Source: CandidateSourceTrending, Candidates: trending},
	)
	fusionSpan.SetAttributes(attribute.Int("recommendation.candidate_count", len(merged)))
	fusionSpan.End()
	span.SetAttributes(attribute.Int("recommendation.candidate_count", len(merged)))
	return CandidateSetSummary{
		Candidates: merged, RecentCount: len(recent), RecentPostIDs: CandidatePostIDs(recent), TrendingCount: len(trending),
	}, nil
}

func candidateCaps(profile Profile, cfg config.RecommendationConfig) config.RecommendationCandidateCaps {
	if len(profile.PositiveVector) == 0 {
		return cfg.Candidates.ColdStart
	}
	return cfg.Candidates.Personalized
}

func mergeCandidateSets(first, second CandidateSetSummary, mergedLimit int) CandidateSetSummary {
	recentPostIDs := append([]uint(nil), first.RecentPostIDs...)
	seenRecent := make(map[uint]struct{}, len(recentPostIDs))
	for _, postID := range recentPostIDs {
		seenRecent[postID] = struct{}{}
	}
	for _, postID := range second.RecentPostIDs {
		if _, exists := seenRecent[postID]; exists {
			continue
		}
		seenRecent[postID] = struct{}{}
		recentPostIDs = append(recentPostIDs, postID)
	}
	mergedCandidates := MergeCandidates(mergedLimit, first.Candidates, second.Candidates)
	interestCount := 0
	for _, candidate := range mergedCandidates {
		if candidate.FromOnboardingInterest {
			interestCount++
		}
	}
	return CandidateSetSummary{
		Candidates:                       mergedCandidates,
		SemanticCount:                    first.SemanticCount + second.SemanticCount,
		FollowingCount:                   first.FollowingCount + second.FollowingCount,
		RecentCount:                      first.RecentCount + second.RecentCount,
		RecentPostIDs:                    recentPostIDs,
		TrendingCount:                    first.TrendingCount + second.TrendingCount,
		OnboardingInterestActive:         first.OnboardingInterestActive || second.OnboardingInterestActive,
		OnboardingInterestCandidateCount: interestCount,
	}
}

func freshGuestExclusions(served ServedHistory) map[uint]struct{} {
	excluded := make(map[uint]struct{}, len(served))
	for postID, item := range served {
		if postID != 0 && (item.Hard || item.Soft) {
			excluded[postID] = struct{}{}
		}
	}
	return excluded
}

func fallbackGuestExclusions(served ServedHistory, selected []SelectedCandidate) map[uint]struct{} {
	excluded := make(map[uint]struct{}, len(served)+len(selected))
	for postID, item := range served {
		if postID != 0 && item.Hard {
			excluded[postID] = struct{}{}
		}
	}
	for _, item := range selected {
		if item.Post.ID != 0 {
			excluded[item.Post.ID] = struct{}{}
		}
	}
	return excluded
}

func annotateGuestFallbackServedState(candidates []RankedCandidate, served ServedHistory) {
	for index := range candidates {
		item, ok := served[candidates[index].Post.ID]
		if !ok || item.Hard || !item.Soft {
			continue
		}
		candidates[index].Candidate.WasSoftServed = true
		candidates[index].Candidate.LastServedAt = item.LastServedAt
	}
}

func loadMaterializedCandidateAuthorContext(ctx context.Context, repository ProfileRepository, userID uint, profile *Profile, candidates []RankedCandidate, loadedAuthors map[uint]struct{}, cfg config.RecommendationConfig) error {
	if profile.AuthorAffinity == nil {
		profile.AuthorAffinity = make(map[uint]float64)
	}
	if profile.FollowingAuthorIDs == nil {
		profile.FollowingAuthorIDs = make(map[uint]struct{})
	}
	if loadedAuthors == nil {
		loadedAuthors = make(map[uint]struct{})
	}
	authorIDs := make([]uint, 0, len(candidates))
	seen := make(map[uint]struct{}, len(candidates))
	for _, candidate := range candidates {
		authorID := candidate.Post.AuthorID
		if authorID == 0 {
			continue
		}
		if _, exists := loadedAuthors[authorID]; exists {
			continue
		}
		if _, exists := seen[authorID]; exists {
			continue
		}
		seen[authorID] = struct{}{}
		authorIDs = append(authorIDs, authorID)
	}
	for _, authorID := range authorIDs {
		loadedAuthors[authorID] = struct{}{}
	}
	if len(authorIDs) == 0 {
		return nil
	}
	if repository == nil {
		return errors.New("recommendation profile repository is nil")
	}
	contextResult, err := repository.LoadAuthorContext(ctx, AuthorContextQuery{
		UserID: userID, AuthorIDs: authorIDs, LoadAffinity: profile.MaterializedInteractionsReady,
		AffinitySaturationScale: cfg.AuthorAffinitySaturationScale,
	})
	if err != nil {
		return err
	}
	for authorID, affinity := range contextResult.Affinity {
		profile.AuthorAffinity[authorID] = affinity
	}
	for authorID := range contextResult.FollowingAuthorIDs {
		profile.FollowingAuthorIDs[authorID] = struct{}{}
	}
	return nil
}

func rankingConfig(cfg config.RecommendationConfig) RankingConfig {
	return RankingConfig{
		SemanticWeight: cfg.SemanticWeight, NegativeSemanticWeight: cfg.NegativeSemanticWeight,
		TrendingWeight: cfg.TrendingWeight, AuthorAffinityWeight: cfg.AuthorAffinityWeight,
		FollowingBonus: cfg.FollowingBonus,
		Trending:       TrendingConfig{MaxAgeDays: cfg.Trending.MaxAgeDays, HalfLifeHours: cfg.Trending.HalfLifeHours, ReplyFactor: cfg.Trending.ReplyFactor},
		Language:       languageConfig(cfg),
	}
}

func fusionConfig(cfg config.RecommendationConfig) FusionConfig {
	return FusionConfig{RankConstant: cfg.Fusion.RankConstant}
}

func selectionConfig(cfg config.RecommendationConfig) SelectionConfig {
	return SelectionConfig{
		OutOfNetworkMinRatio: cfg.OutOfNetworkMinRatio,
		Diversity: DiversityConfig{
			Enabled: cfg.Diversity.Enabled, AuthorWindowSize: cfg.Diversity.AuthorWindowSize,
			MaxSameAuthorInWindow:      cfg.Diversity.MaxSameAuthorInWindow,
			SemanticDuplicateThreshold: cfg.Diversity.SemanticDuplicateThreshold,
			SemanticDuplicatePenalty:   cfg.Diversity.SemanticDuplicatePenalty,
		},
		Exploration: ExplorationConfig{
			Ratio: cfg.Exploration.Ratio, MaxSlots: cfg.Exploration.MaxSlots,
			RecentWindowDays:    cfg.Exploration.RecentWindowDays,
			NovelPostMaxAgeDays: cfg.Exploration.NovelPostMaxAgeDays,
		},
	}
}

func languageConfig(cfg config.RecommendationConfig) LanguageConfig {
	return LanguageConfig{
		Enabled: cfg.LanguageAffinity.Enabled, Weight: cfg.LanguageAffinity.Weight,
		EvidenceSaturationScale: cfg.LanguageAffinity.EvidenceSaturationScale,
		MaxBehaviorShare:        cfg.LanguageAffinity.MaxBehaviorShare,
	}
}

func profileFeatures(profile Profile) ProfileFeatures {
	return ProfileFeatures{
		PositiveVector: profile.PositiveVector, NegativeVector: profile.NegativeVector,
		NegativeConfidence: profile.NegativeConfidence, OnboardingInterestStrength: profile.OnboardingInterestStrength,
		AuthorAffinity:     profile.AuthorAffinity,
		FollowingAuthorIDs: profile.FollowingAuthorIDs,
	}
}

package recommendation

import (
	"context"
	"errors"
	"time"

	"Go.exchange/config"
)

func buildCandidateSet(ctx context.Context, repository CandidateRepository, servingVersion string, userID uint, profile Profile, served ServedHistory, now time.Time, cfg config.RecommendationConfig, softOnly bool) (CandidateSetSummary, error) {
	if repository == nil {
		return CandidateSetSummary{}, errors.New("recommendation candidate repository is nil")
	}
	caps := candidateCaps(profile, cfg)
	query := CandidateQuery{
		UserID: userID, ServingVersion: servingVersion, Now: now,
		PositiveVector: profile.PositiveVector, SemanticRecentRatio: cfg.SemanticRecall.RecentRatio,
		SemanticRecentCutoff: now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays),
		TrendingCutoff:       now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor:  cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		Served: served, SoftOnly: softOnly,
		MaterializedInteractionsReady: profile.MaterializedInteractionsReady,
		InteractedPostIDs:             profile.InteractedPostIDs,
	}
	query.Limit = caps.Semantic
	semantic, err := repository.LoadSemanticCandidates(ctx, query)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Following
	following, err := repository.LoadFollowingCandidates(ctx, query)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Recent
	recent, err := repository.LoadRecentCandidates(ctx, query)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Trending
	trending, err := repository.LoadTrendingCandidates(ctx, query)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	merged := FuseCandidates(
		caps.Merged, fusionConfig(cfg),
		CandidateSet{Source: CandidateSourceSemantic, Candidates: semantic},
		CandidateSet{Source: CandidateSourceFollowing, Candidates: following},
		CandidateSet{Source: CandidateSourceRecent, Candidates: recent},
		CandidateSet{Source: CandidateSourceTrending, Candidates: trending},
	)
	for index := range merged {
		if item, ok := served[merged[index].PostID]; ok {
			merged[index].LastServedAt = item.LastServedAt
			merged[index].WasSoftServed = softOnly && item.Soft && !item.Hard
		}
	}
	return CandidateSetSummary{
		Candidates: merged, SemanticCount: len(semantic), FollowingCount: len(following),
		RecentCount: len(recent), RecentPostIDs: CandidatePostIDs(recent), TrendingCount: len(trending),
	}, nil
}

func buildPublicCandidateSet(ctx context.Context, repository CandidateRepository, now time.Time, cfg config.RecommendationConfig, excluded map[uint]struct{}) (CandidateSetSummary, error) {
	if repository == nil {
		return CandidateSetSummary{}, errors.New("recommendation candidate repository is nil")
	}
	caps := cfg.Candidates.ColdStart
	query := PublicCandidateQuery{
		Now: now, TrendingCutoff: now.AddDate(0, 0, -cfg.Trending.MaxAgeDays),
		TrendingReplyFactor: cfg.Trending.ReplyFactor, TrendingHalfLifeHours: cfg.Trending.HalfLifeHours,
		ExcludedPostIDs: excluded,
	}
	query.Limit = caps.Recent
	recent, err := repository.LoadPublicRecentCandidates(ctx, query)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	query.Limit = caps.Trending
	trending, err := repository.LoadPublicTrendingCandidates(ctx, query)
	if err != nil {
		return CandidateSetSummary{}, err
	}
	merged := FuseCandidates(
		caps.Merged, fusionConfig(cfg),
		CandidateSet{Source: CandidateSourceRecent, Candidates: recent},
		CandidateSet{Source: CandidateSourceTrending, Candidates: trending},
	)
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
	return CandidateSetSummary{
		Candidates:     MergeCandidates(mergedLimit, first.Candidates, second.Candidates),
		SemanticCount:  first.SemanticCount + second.SemanticCount,
		FollowingCount: first.FollowingCount + second.FollowingCount,
		RecentCount:    first.RecentCount + second.RecentCount,
		RecentPostIDs:  recentPostIDs,
		TrendingCount:  first.TrendingCount + second.TrendingCount,
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
		NegativeConfidence: profile.NegativeConfidence, AuthorAffinity: profile.AuthorAffinity,
		FollowingAuthorIDs: profile.FollowingAuthorIDs,
	}
}

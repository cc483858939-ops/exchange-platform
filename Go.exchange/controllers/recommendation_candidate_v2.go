package controllers

import (
	"errors"
	"math"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type servedPost struct {
	LastServedAt time.Time
	Hard         bool
	Soft         bool
}

type recommendationCandidateSet struct {
	Candidates     []embeddingCandidate
	SemanticCount  int
	FollowingCount int
	RecentCount    int
	RecentPostIDs  []uint
	TrendingCount  int
}

type hydratedRecommendationCandidate struct {
	Candidate           embeddingCandidate
	Post                models.Post
	PostArticle         *models.PostArticle
	Embedding           []float32
	Breakdown           recommendationScoreBreakdown
	ExplorationSemantic float64
	IsInNetwork         bool
	IsNovelAuthor       bool
}

func loadRecommendationServedHistory(userID uint, now time.Time, cfg config.RecommendationConfig) (map[uint]servedPost, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	type servedRow struct {
		PostID       uint
		LastServedAt time.Time
	}
	var rows []servedRow
	start := now.AddDate(0, 0, -cfg.ServedSoftLookbackDays)
	err := global.Db.Table("recommendation_result_traces AS rt").
		Select("rt.post_id, MAX(rt.created_at) AS last_served_at").
		Joins("JOIN recommendation_requests AS rr ON rr.request_id = rt.request_id").
		Where("rr.user_id = ? AND rt.created_at >= ?", userID, start).
		Group("rt.post_id").
		Order("last_served_at DESC").
		Limit(cfg.ServedHistoryLimit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	history := make(map[uint]servedPost, len(rows))
	hardStart := now.Add(-time.Duration(cfg.ServedHardExclusionMinutes) * time.Minute)
	for _, row := range rows {
		if row.PostID == 0 {
			continue
		}
		history[row.PostID] = servedPost{
			LastServedAt: row.LastServedAt,
			Hard:         !row.LastServedAt.Before(hardStart),
			Soft:         row.LastServedAt.After(start) && row.LastServedAt.Before(hardStart),
		}
	}
	return history, nil
}

func recommendationEligibilityQuery(query *gorm.DB, userID uint, served map[uint]servedPost, now time.Time, softOnly bool, useMaterializedInteractions bool) *gorm.DB {
	negative := global.Db.Table("post_behaviors AS ni").
		Select("1").
		Where("ni.user_id = ? AND ni.post_id = posts.id AND ni.action = ? AND ni.active = TRUE",
			userID, eventing.RecommendationBehaviorActionNotInterested)
	laterLike := global.Db.Table("post_reaction AS ar").
		Select("1").
		Where("ar.user_id = ? AND ar.post_id = ni.post_id AND ar.liked = TRUE AND ar.state_changed_at > ni.last_seen_at", userID)
	laterReply := global.Db.Table("post_behaviors AS rb").
		Select("1").
		Where("rb.user_id = ? AND rb.post_id = ni.post_id AND rb.action = ? AND rb.active = TRUE AND rb.last_seen_at > ni.last_seen_at",
			userID, PostBehaviorActionReply)
	negative = negative.Where("NOT EXISTS (?)", laterLike).Where("NOT EXISTS (?)", laterReply)
	query = publicPostScope(query, now).
		Where("posts.reply_to_post_id IS NULL").
		Joins("LEFT JOIN post_articles AS pa_recommendation ON pa_recommendation.post_id = posts.id").
		Where(
			"EXISTS (SELECT 1 FROM users AS recommendation_authors "+
				"WHERE recommendation_authors.id = posts.author_id "+
				"AND recommendation_authors.deleted_at IS NULL)",
		).
		Where("posts.author_id <> ?", userID).
		Where("NOT EXISTS (?)", negative)
	if useMaterializedInteractions {
		interacted := global.Db.Table("user_post_reco_states AS rs").
			Select("1").
			Where("rs.user_id = ? AND rs.post_id = posts.id AND rs.interacted = TRUE", userID)
		query = query.Where("NOT EXISTS (?)", interacted)
	}

	excluded := make(map[uint]struct{}, len(served))
	for id, item := range served {
		if softOnly {
			if item.Soft && !item.Hard {
				continue
			}
		} else {
			excluded[id] = struct{}{}
		}
	}
	if softOnly {
		softIDs := make([]uint, 0, len(served))
		for id, item := range served {
			if item.Soft && !item.Hard {
				softIDs = append(softIDs, id)
			}
		}
		if len(softIDs) == 0 {
			return query.Where("1 = 0")
		}
		query = query.Where("posts.id IN ?", softIDs)
	}
	if ids := postIDList(excluded); len(ids) > 0 {
		query = query.Where("posts.id NOT IN ?", ids)
	}
	return query
}

func applyLegacyProfileInteractionExclusion(query *gorm.DB, profile userInterestProfile) *gorm.DB {
	if profile.MaterializedInteractionsReady {
		return query
	}
	if ids := postIDList(profile.InteractedPostIDs); len(ids) > 0 {
		return query.Where("posts.id NOT IN ?", ids)
	}
	return query
}

type semanticCandidateRow struct {
	PostID                     uint
	PositiveSemanticSimilarity float64
}

func recommendationSemanticQuota(cap int, recentRatio float64) (int, int) {
	if cap <= 0 {
		return 0, 0
	}
	if cap == 1 {
		return 1, 0
	}
	if recentRatio <= 0 || recentRatio >= 1 {
		recentRatio = 0.80
	}
	recentCap := int(math.Round(float64(cap) * recentRatio))
	if recentCap < 1 {
		recentCap = 1
	}
	if recentCap > cap-1 {
		recentCap = cap - 1
	}
	return recentCap, cap - recentCap
}

func loadRecommendationSemanticCandidates(userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]embeddingCandidate, error) {
	if len(profile.PositiveVector) == 0 || cap <= 0 {
		return nil, nil
	}

	recentCap, evergreenCap := recommendationSemanticQuota(cap, cfg.SemanticRecall.RecentRatio)
	cutoff := now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays)
	recent, err := loadRecommendationSemanticPool(userID, profile, served, now, softOnly, cutoff, ">=", recentCap, nil)
	if err != nil {
		return nil, err
	}
	selectedIDs := make(map[uint]struct{}, len(recent)+evergreenCap)
	for _, candidate := range recent {
		selectedIDs[candidate.PostID] = struct{}{}
	}

	evergreen, err := loadRecommendationSemanticPool(userID, profile, served, now, softOnly, cutoff, "<", evergreenCap, selectedIDs)
	if err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, cap)
	result = append(result, recent...)
	for _, candidate := range evergreen {
		selectedIDs[candidate.PostID] = struct{}{}
	}
	result = append(result, evergreen...)

	remaining := cap - len(result)
	if remaining > 0 {
		backfill, err := loadRecommendationSemanticPool(userID, profile, served, now, softOnly, time.Time{}, "", remaining, selectedIDs)
		if err != nil {
			return nil, err
		}
		result = append(result, backfill...)
	}
	return result, nil
}

func loadRecommendationSemanticPool(userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, softOnly bool, cutoff time.Time, comparison string, cap int, excluded map[uint]struct{}) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	queryVector := pgvector.NewVector(profile.PositiveVector)
	query := recommendationEligibilityQuery(
		global.Db.Table("post_embeddings AS ae").
			Select("ae.post_id, 1 - (ae.embedding <=> ?) AS positive_semantic_similarity", queryVector).
			Joins("JOIN posts ON posts.id = ae.post_id").
			Where("ae.version = ? AND ae.dimensions = ?", config.ActiveEmbeddingVersion(), len(profile.PositiveVector)),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	if comparison != "" {
		query = query.Where(effectivePublishedAtSQL("posts", "pa_recommendation")+" "+comparison+" ?", cutoff)
	}
	if ids := postIDList(excluded); len(ids) > 0 {
		query = query.Where("posts.id NOT IN ?", ids)
	}

	var rows []semanticCandidateRow
	if err := query.Clauses(clause.OrderBy{
		Expression: clause.Expr{
			SQL:  "ae.embedding <=> ? ASC, posts.id DESC",
			Vars: []interface{}{queryVector},
		},
	}).Limit(cap).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, embeddingCandidate{PostID: row.PostID, PositiveSemanticSimilarity: clampRecommendationSimilarity(row.PositiveSemanticSimilarity), FromSemantic: true})
	}
	return result, nil
}

func loadRecommendationFollowingCandidates(userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := recommendationEligibilityQuery(
		global.Db.Table("posts").
			Select("posts.id").
			Joins("JOIN user_follows AS uf ON uf.following_id = posts.author_id AND uf.follower_id = ?", userID),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	var ids []uint
	if err := query.Order(effectivePublishedAtSQL("posts", "pa_recommendation")+" DESC, posts.id DESC").Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, embeddingCandidate{PostID: id, FromFollowing: true})
	}
	return result, nil
}

func loadRecommendationSourceCandidates(userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, order interface{}, cap int, source string) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := recommendationEligibilityQuery(global.Db.Table("posts").Select("posts.id"), userID, served, now, softOnly, profile.MaterializedInteractionsReady)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(ids))
	for _, id := range ids {
		candidate := embeddingCandidate{PostID: id}
		switch source {
		case "recent":
			candidate.FromRecent = true
		}
		result = append(result, candidate)
	}
	return result, nil
}

func loadRecommendationCandidateSet(userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool) (recommendationCandidateSet, error) {
	if global.Db == nil {
		return recommendationCandidateSet{}, errors.New("database is not initialized")
	}
	caps := recommendationCandidateCaps(profile, cfg)
	semantic, err := loadRecommendationSemanticCandidates(userID, profile, served, now, cfg, softOnly, caps.Semantic)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	following, err := loadRecommendationFollowingCandidates(userID, profile, served, now, cfg, softOnly, caps.Following)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	recent, err := loadRecommendationSourceCandidates(userID, profile, served, now, cfg, softOnly, effectivePublishedAtSQL("posts", "pa_recommendation")+" DESC, posts.id DESC", caps.Recent, "recent")
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	trending, err := loadRecommendationTrendingCandidates(userID, profile, served, now, cfg, softOnly, caps.Trending)
	if err != nil {
		return recommendationCandidateSet{}, err
	}

	merged := fuseRecommendationCandidates(
		caps.Merged,
		cfg.Fusion.RankConstant,
		recommendationRecallList{Source: recommendationRecallSourceSemantic, Candidates: semantic},
		recommendationRecallList{Source: recommendationRecallSourceFollowing, Candidates: following},
		recommendationRecallList{Source: recommendationRecallSourceRecent, Candidates: recent},
		recommendationRecallList{Source: recommendationRecallSourceTrending, Candidates: trending},
	)
	for index := range merged {
		if item, ok := served[merged[index].PostID]; ok {
			merged[index].LastServedAt = item.LastServedAt
			merged[index].WasSoftServed = softOnly && item.Soft && !item.Hard
		}
	}
	return recommendationCandidateSet{
		Candidates: merged, SemanticCount: len(semantic), FollowingCount: len(following),
		RecentCount: len(recent), RecentPostIDs: recommendationCandidatePostIDs(recent), TrendingCount: len(trending),
	}, nil
}

func loadRecommendationTrendingCandidates(userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	cutoff := now.AddDate(0, 0, -cfg.Trending.MaxAgeDays)
	query := recommendationEligibilityQuery(
		global.Db.Table("posts").Select("posts.id"),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	).Where(effectivePublishedAtSQL("posts", "pa_recommendation")+" >= ?", cutoff).
		Where("posts.like_count > 0 OR posts.reply_count > 0")
	query = applyLegacyProfileInteractionExclusion(query, profile)
	effectiveTime := effectivePublishedAtSQL("posts", "pa_recommendation")
	order := gorm.Expr(`
(
    LN(1 + GREATEST(posts.like_count, 0))
    + ? * LN(1 + GREATEST(posts.reply_count, 0))
)
*
EXP(
    -LN(2)
    * GREATEST(EXTRACT(EPOCH FROM (? - `+effectiveTime+`)) / 3600.0, 0)
    / ?
)
DESC,
`+effectiveTime+` DESC,
posts.id DESC`, cfg.Trending.ReplyFactor, now.UTC(), cfg.Trending.HalfLifeHours)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, embeddingCandidate{PostID: id, FromTrending: true})
	}
	return result, nil
}

func recommendationCandidateCaps(profile userInterestProfile, cfg config.RecommendationConfig) config.RecommendationCandidateCaps {
	if len(profile.PositiveVector) == 0 {
		return cfg.Candidates.ColdStart
	}
	return cfg.Candidates.Personalized
}

func mergeEmbeddingCandidates(limit int, sources ...[]embeddingCandidate) []embeddingCandidate {
	// mergeEmbeddingCandidates performs stable candidate-set union.
	// Recall-list fusion must use fuseRecommendationCandidates instead.
	if limit <= 0 {
		return nil
	}
	merged := make([]embeddingCandidate, 0, limit)
	byID := make(map[uint]int, limit)
	for _, source := range sources {
		for _, candidate := range source {
			if candidate.PostID == 0 {
				continue
			}
			if index, ok := byID[candidate.PostID]; ok {
				current := &merged[index]
				current.FromSemantic = current.FromSemantic || candidate.FromSemantic
				current.FromFollowing = current.FromFollowing || candidate.FromFollowing
				current.FromRecent = current.FromRecent || candidate.FromRecent
				current.FromTrending = current.FromTrending || candidate.FromTrending
				current.SemanticRank = recommendationMinNonZeroRank(current.SemanticRank, candidate.SemanticRank)
				current.FollowingRank = recommendationMinNonZeroRank(current.FollowingRank, candidate.FollowingRank)
				current.RecentRank = recommendationMinNonZeroRank(current.RecentRank, candidate.RecentRank)
				current.TrendingRank = recommendationMinNonZeroRank(current.TrendingRank, candidate.TrendingRank)
				if candidate.FusionScore > current.FusionScore {
					current.FusionScore = candidate.FusionScore
				}
				if candidate.SourceCount > current.SourceCount {
					current.SourceCount = candidate.SourceCount
				}
				current.WasSoftServed = current.WasSoftServed || candidate.WasSoftServed
				if current.LastServedAt.IsZero() || (!candidate.LastServedAt.IsZero() && candidate.LastServedAt.Before(current.LastServedAt)) {
					current.LastServedAt = candidate.LastServedAt
				}
				if candidate.FromSemantic {
					current.PositiveSemanticSimilarity = candidate.PositiveSemanticSimilarity
				}
				continue
			}
			if len(merged) >= limit {
				continue
			}
			byID[candidate.PostID] = len(merged)
			merged = append(merged, candidate)
		}
	}
	return merged
}

func mergeCandidateSets(first, second recommendationCandidateSet, mergedLimit int) recommendationCandidateSet {
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
	return recommendationCandidateSet{
		Candidates:     mergeEmbeddingCandidates(mergedLimit, first.Candidates, second.Candidates),
		SemanticCount:  first.SemanticCount + second.SemanticCount,
		FollowingCount: first.FollowingCount + second.FollowingCount,
		RecentCount:    first.RecentCount + second.RecentCount,
		RecentPostIDs:  recentPostIDs,
		TrendingCount:  first.TrendingCount + second.TrendingCount,
	}
}

func recommendationCandidatePostIDs(candidates []embeddingCandidate) []uint {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PostID != 0 {
			ids = append(ids, candidate.PostID)
		}
	}
	return ids
}

func hydrateRecommendationCandidates(candidates []embeddingCandidate, now time.Time) ([]hydratedRecommendationCandidate, error) {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PostID != 0 {
			ids = append(ids, candidate.PostID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	query := publicPostScope(global.Db.Model(&models.Post{}).Select(publicPostSelectColumns).Where("posts.id IN ?", ids), now)
	var posts []models.Post
	if err := preloadPostAuthor(query).Find(&posts).Error; err != nil {
		return nil, err
	}
	validPosts := make([]models.Post, 0, len(posts))
	validPostIDs := make([]uint, 0, len(posts))
	for _, post := range posts {
		if _, err := publicAuthorFromPost(post); err != nil {
			continue
		}
		validPosts = append(validPosts, post)
		validPostIDs = append(validPostIDs, post.ID)
	}
	if len(validPostIDs) == 0 {
		return nil, nil
	}
	embeddings, err := loadRecommendationPostEmbeddings(validPostIDs, config.ActiveEmbeddingVersion())
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]models.Post, len(validPosts))
	postArticles, err := loadPostArticles(validPosts)
	if err != nil {
		return nil, err
	}
	for _, post := range validPosts {
		byID[post.ID] = post
	}
	result := make([]hydratedRecommendationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		post, ok := byID[candidate.PostID]
		if !ok {
			continue
		}
		result = append(result, hydratedRecommendationCandidate{Candidate: candidate, Post: post, PostArticle: postArticles[post.ID], Embedding: embeddings[candidate.PostID]})
	}
	return result, nil
}

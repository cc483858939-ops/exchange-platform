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

type servedArticle struct {
	LastServedAt time.Time
	Hard         bool
	Soft         bool
}

type recommendationCandidateSet struct {
	Candidates     []embeddingCandidate
	SemanticCount  int
	FollowingCount int
	RecentCount    int
	TrendingCount  int
}

type hydratedRecommendationCandidate struct {
	Candidate     embeddingCandidate
	Article       models.Article
	Embedding     []float32
	Breakdown     recommendationScoreBreakdown
	IsInNetwork   bool
	IsNovelAuthor bool
}

func loadRecommendationServedHistory(userID uint, now time.Time, cfg config.RecommendationConfig) (map[uint]servedArticle, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	type servedRow struct {
		ArticleID    uint
		LastServedAt time.Time
	}
	var rows []servedRow
	start := now.AddDate(0, 0, -cfg.ServedSoftLookbackDays)
	err := global.Db.Table("recommendation_result_traces AS rt").
		Select("rt.article_id, MAX(rt.created_at) AS last_served_at").
		Joins("JOIN recommendation_requests AS rr ON rr.request_id = rt.request_id").
		Where("rr.user_id = ? AND rt.created_at >= ?", userID, start).
		Group("rt.article_id").
		Order("last_served_at DESC").
		Limit(cfg.ServedHistoryLimit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	history := make(map[uint]servedArticle, len(rows))
	hardStart := now.Add(-time.Duration(cfg.ServedHardExclusionMinutes) * time.Minute)
	for _, row := range rows {
		if row.ArticleID == 0 {
			continue
		}
		history[row.ArticleID] = servedArticle{
			LastServedAt: row.LastServedAt,
			Hard:         !row.LastServedAt.Before(hardStart),
			Soft:         row.LastServedAt.After(start) && row.LastServedAt.Before(hardStart),
		}
	}
	return history, nil
}

func recommendationEligibilityQuery(query *gorm.DB, userID uint, served map[uint]servedArticle, now time.Time, softOnly bool, useMaterializedInteractions bool) *gorm.DB {
	negative := global.Db.Table("article_behaviors AS ni").
		Select("1").
		Where("ni.user_id = ? AND ni.article_id = articles.id AND ni.action = ? AND ni.active = TRUE",
			userID, eventing.RecommendationBehaviorActionNotInterested)
	laterLike := global.Db.Table("article_reaction AS ar").
		Select("1").
		Where("ar.user_id = ? AND ar.article_id = ni.article_id AND ar.liked = TRUE AND ar.state_changed_at > ni.last_seen_at", userID)
	laterReply := global.Db.Table("article_behaviors AS rb").
		Select("1").
		Where("rb.user_id = ? AND rb.article_id = ni.article_id AND rb.action = ? AND rb.active = TRUE AND rb.last_seen_at > ni.last_seen_at",
			userID, ArticleBehaviorActionReply)
	negative = negative.Where("NOT EXISTS (?)", laterLike).Where("NOT EXISTS (?)", laterReply)
	query = publicArticleScope(query, now).
		Where(
			"EXISTS (SELECT 1 FROM users AS recommendation_authors "+
				"WHERE recommendation_authors.id = articles.author_id "+
				"AND recommendation_authors.deleted_at IS NULL)",
		).
		Where("articles.author_id <> ?", userID).
		Where("NOT EXISTS (?)", negative)
	if useMaterializedInteractions {
		interacted := global.Db.Table("user_article_reco_states AS rs").
			Select("1").
			Where("rs.user_id = ? AND rs.article_id = articles.id AND rs.interacted = TRUE", userID)
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
		query = query.Where("articles.id IN ?", softIDs)
	}
	if ids := articleIDList(excluded); len(ids) > 0 {
		query = query.Where("articles.id NOT IN ?", ids)
	}
	return query
}

func applyLegacyProfileInteractionExclusion(query *gorm.DB, profile userInterestProfile) *gorm.DB {
	if profile.MaterializedInteractionsReady {
		return query
	}
	if ids := articleIDList(profile.InteractedArticleIDs); len(ids) > 0 {
		return query.Where("articles.id NOT IN ?", ids)
	}
	return query
}

type semanticCandidateRow struct {
	ArticleID                  uint
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

func loadRecommendationSemanticCandidates(userID uint, profile userInterestProfile, served map[uint]servedArticle, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]embeddingCandidate, error) {
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
		selectedIDs[candidate.ArticleID] = struct{}{}
	}

	evergreen, err := loadRecommendationSemanticPool(userID, profile, served, now, softOnly, cutoff, "<", evergreenCap, selectedIDs)
	if err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, cap)
	result = append(result, recent...)
	for _, candidate := range evergreen {
		selectedIDs[candidate.ArticleID] = struct{}{}
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

func loadRecommendationSemanticPool(userID uint, profile userInterestProfile, served map[uint]servedArticle, now time.Time, softOnly bool, cutoff time.Time, comparison string, cap int, excluded map[uint]struct{}) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	queryVector := pgvector.NewVector(profile.PositiveVector)
	query := recommendationEligibilityQuery(
		global.Db.Table("article_embeddings AS ae").
			Select("ae.article_id, 1 - (ae.embedding <=> ?) AS positive_semantic_similarity", queryVector).
			Joins("JOIN articles ON articles.id = ae.article_id").
			Where("ae.version = ? AND ae.dimensions = ?", config.ActiveEmbeddingVersion(), len(profile.PositiveVector)),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	if comparison != "" {
		query = query.Where("articles.published_at "+comparison+" ?", cutoff)
	}
	if ids := articleIDList(excluded); len(ids) > 0 {
		query = query.Where("articles.id NOT IN ?", ids)
	}

	var rows []semanticCandidateRow
	if err := query.Clauses(clause.OrderBy{
		Expression: clause.Expr{
			SQL:  "ae.embedding <=> ? ASC, articles.id DESC",
			Vars: []interface{}{queryVector},
		},
	}).Limit(cap).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, embeddingCandidate{ArticleID: row.ArticleID, PositiveSemanticSimilarity: clampRecommendationSimilarity(row.PositiveSemanticSimilarity), FromSemantic: true})
	}
	return result, nil
}

func loadRecommendationFollowingCandidates(userID uint, profile userInterestProfile, served map[uint]servedArticle, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := recommendationEligibilityQuery(
		global.Db.Table("articles").
			Select("articles.id").
			Joins("JOIN user_follows AS uf ON uf.following_id = articles.author_id AND uf.follower_id = ?", userID),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	var ids []uint
	if err := query.Order("articles.published_at DESC, articles.id DESC").Limit(cap).Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, embeddingCandidate{ArticleID: id, FromFollowing: true})
	}
	return result, nil
}

func loadRecommendationSourceCandidates(userID uint, profile userInterestProfile, served map[uint]servedArticle, now time.Time, cfg config.RecommendationConfig, softOnly bool, order interface{}, cap int, source string) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := recommendationEligibilityQuery(global.Db.Table("articles").Select("articles.id"), userID, served, now, softOnly, profile.MaterializedInteractionsReady)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(ids))
	for _, id := range ids {
		candidate := embeddingCandidate{ArticleID: id}
		switch source {
		case "recent":
			candidate.FromRecent = true
		}
		result = append(result, candidate)
	}
	return result, nil
}

func loadRecommendationCandidateSet(userID uint, profile userInterestProfile, served map[uint]servedArticle, now time.Time, cfg config.RecommendationConfig, softOnly bool) (recommendationCandidateSet, error) {
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
	recent, err := loadRecommendationSourceCandidates(userID, profile, served, now, cfg, softOnly, "articles.published_at DESC, articles.id DESC", caps.Recent, "recent")
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	trending, err := loadRecommendationTrendingCandidates(userID, profile, served, now, cfg, softOnly, caps.Trending)
	if err != nil {
		return recommendationCandidateSet{}, err
	}

	merged := mergeEmbeddingCandidates(caps.Merged, semantic, following, recent, trending)
	for index := range merged {
		if item, ok := served[merged[index].ArticleID]; ok {
			merged[index].LastServedAt = item.LastServedAt
			merged[index].WasSoftServed = softOnly && item.Soft && !item.Hard
		}
	}
	return recommendationCandidateSet{
		Candidates: merged, SemanticCount: len(semantic), FollowingCount: len(following),
		RecentCount: len(recent), TrendingCount: len(trending),
	}, nil
}

func loadRecommendationTrendingCandidates(userID uint, profile userInterestProfile, served map[uint]servedArticle, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]embeddingCandidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	cutoff := now.AddDate(0, 0, -cfg.Trending.MaxAgeDays)
	query := recommendationEligibilityQuery(
		global.Db.Table("articles").Select("articles.id"),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	).Where("articles.published_at >= ?", cutoff).
		Where("articles.like_count > 0 OR articles.comment_count > 0")
	query = applyLegacyProfileInteractionExclusion(query, profile)
	order := gorm.Expr(`
(
    LN(1 + GREATEST(articles.like_count, 0))
    + ? * LN(1 + GREATEST(articles.comment_count, 0))
)
*
EXP(
    -LN(2)
    * GREATEST(EXTRACT(EPOCH FROM (? - articles.published_at)) / 3600.0, 0)
    / ?
)
DESC,
articles.published_at DESC,
articles.id DESC`, cfg.Trending.CommentFactor, now.UTC(), cfg.Trending.HalfLifeHours)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]embeddingCandidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, embeddingCandidate{ArticleID: id, FromTrending: true})
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
	if limit <= 0 {
		return nil
	}
	merged := make([]embeddingCandidate, 0, limit)
	byID := make(map[uint]int, limit)
	for _, source := range sources {
		for _, candidate := range source {
			if candidate.ArticleID == 0 {
				continue
			}
			if index, ok := byID[candidate.ArticleID]; ok {
				current := &merged[index]
				current.FromSemantic = current.FromSemantic || candidate.FromSemantic
				current.FromFollowing = current.FromFollowing || candidate.FromFollowing
				current.FromRecent = current.FromRecent || candidate.FromRecent
				current.FromTrending = current.FromTrending || candidate.FromTrending
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
			byID[candidate.ArticleID] = len(merged)
			merged = append(merged, candidate)
		}
	}
	return merged
}

func mergeCandidateSets(first, second recommendationCandidateSet, mergedLimit int) recommendationCandidateSet {
	return recommendationCandidateSet{
		Candidates:     mergeEmbeddingCandidates(mergedLimit, first.Candidates, second.Candidates),
		SemanticCount:  first.SemanticCount + second.SemanticCount,
		FollowingCount: first.FollowingCount + second.FollowingCount,
		RecentCount:    first.RecentCount + second.RecentCount,
		TrendingCount:  first.TrendingCount + second.TrendingCount,
	}
}

func hydrateRecommendationCandidates(candidates []embeddingCandidate, now time.Time) ([]hydratedRecommendationCandidate, error) {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ArticleID != 0 {
			ids = append(ids, candidate.ArticleID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	query := publicArticleScope(global.Db.Model(&models.Article{}).Select(publicArticleSelectColumns).Where("articles.id IN ?", ids), now)
	var articles []models.Article
	if err := preloadArticleAuthor(query).Find(&articles).Error; err != nil {
		return nil, err
	}
	validArticles := make([]models.Article, 0, len(articles))
	validArticleIDs := make([]uint, 0, len(articles))
	for _, article := range articles {
		if _, err := publicAuthorFromArticle(article); err != nil {
			continue
		}
		validArticles = append(validArticles, article)
		validArticleIDs = append(validArticleIDs, article.ID)
	}
	if len(validArticleIDs) == 0 {
		return nil, nil
	}
	embeddings, err := loadRecommendationArticleEmbeddings(validArticleIDs, config.ActiveEmbeddingVersion())
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]models.Article, len(validArticles))
	for _, article := range validArticles {
		byID[article.ID] = article
	}
	result := make([]hydratedRecommendationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		article, ok := byID[candidate.ArticleID]
		if !ok {
			continue
		}
		result = append(result, hydratedRecommendationCandidate{Candidate: candidate, Article: article, Embedding: embeddings[candidate.ArticleID]})
	}
	return result, nil
}

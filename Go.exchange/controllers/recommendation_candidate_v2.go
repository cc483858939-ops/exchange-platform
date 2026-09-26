package controllers

import (
	"errors"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/models"
	"Go.exchange/recommendation"

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
	Candidates     []recommendation.Candidate
	SemanticCount  int
	FollowingCount int
	RecentCount    int
	RecentPostIDs  []uint
	TrendingCount  int
}

// recommendationEligibilityQuery contains post and viewer eligibility shared
// by recommendation recall sources. Serving-embedding version gates belong to
// semantic recall; recent, following, and trending candidates can be hydrated
// and ranked without a matching embedding.
func recommendationEligibilityQuery(db *gorm.DB, query *gorm.DB, userID uint, served map[uint]servedPost, now time.Time, softOnly bool, useMaterializedInteractions bool) *gorm.DB {
	negative := db.Table("post_behaviors AS ni").
		Select("1").
		Where("ni.user_id = ? AND ni.post_id = posts.id AND ni.action = ? AND ni.active = TRUE",
			userID, eventing.RecommendationBehaviorActionNotInterested)
	laterLike := db.Table("post_reaction AS ar").
		Select("1").
		Where("ar.user_id = ? AND ar.post_id = ni.post_id AND ar.liked = TRUE AND ar.state_changed_at > ni.last_seen_at", userID)
	laterReply := db.Table("post_behaviors AS rb").
		Select("1").
		Where("rb.user_id = ? AND rb.post_id = ni.post_id AND rb.action = ? AND rb.active = TRUE AND rb.last_seen_at > ni.last_seen_at",
			userID, PostBehaviorActionReply)
	negative = negative.Where("NOT EXISTS (?)", laterLike).Where("NOT EXISTS (?)", laterReply)
	query = publicPostScope(query, now).
		Where("posts.reply_to_post_id IS NULL").
		Where(
			"EXISTS (SELECT 1 FROM users AS recommendation_authors "+
				"WHERE recommendation_authors.id = posts.author_id "+
				"AND recommendation_authors.deleted_at IS NULL)",
		).
		Where("posts.author_id <> ?", userID).
		Where("NOT EXISTS (?)", negative)
	if useMaterializedInteractions {
		interacted := db.Table("user_post_reco_states AS rs").
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

func loadRecommendationSemanticCandidates(db *gorm.DB, servingVersion string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]recommendation.Candidate, error) {
	if len(profile.PositiveVector) == 0 || cap <= 0 {
		return nil, nil
	}

	recentCap, evergreenCap := recommendation.SemanticRecallQuota(cap, cfg.SemanticRecall.RecentRatio)
	cutoff := now.AddDate(0, 0, -cfg.SemanticRecall.RecentWindowDays)
	recent, err := loadRecommendationSemanticPool(db, servingVersion, userID, profile, served, now, softOnly, cutoff, ">=", recentCap, nil)
	if err != nil {
		return nil, err
	}
	selectedIDs := make(map[uint]struct{}, len(recent)+evergreenCap)
	for _, candidate := range recent {
		selectedIDs[candidate.PostID] = struct{}{}
	}

	evergreen, err := loadRecommendationSemanticPool(db, servingVersion, userID, profile, served, now, softOnly, cutoff, "<", evergreenCap, selectedIDs)
	if err != nil {
		return nil, err
	}
	result := make([]recommendation.Candidate, 0, cap)
	result = append(result, recent...)
	for _, candidate := range evergreen {
		selectedIDs[candidate.PostID] = struct{}{}
	}
	result = append(result, evergreen...)

	remaining := cap - len(result)
	if remaining > 0 {
		backfill, err := loadRecommendationSemanticPool(db, servingVersion, userID, profile, served, now, softOnly, time.Time{}, "", remaining, selectedIDs)
		if err != nil {
			return nil, err
		}
		result = append(result, backfill...)
	}
	return result, nil
}

func loadRecommendationSemanticPool(db *gorm.DB, servingVersion string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, softOnly bool, cutoff time.Time, comparison string, cap int, excluded map[uint]struct{}) ([]recommendation.Candidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	queryVector := pgvector.NewVector(profile.PositiveVector)
	query := recommendationEligibilityQuery(
		db,
		db.Table("post_embeddings AS ae").
			Select("ae.post_id, 1 - (ae.embedding <=> ?) AS positive_semantic_similarity", queryVector).
			Joins("JOIN posts ON posts.id = ae.post_id").
			Where("ae.version = ? AND ae.dimensions = ?", servingVersion, len(profile.PositiveVector)),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	if comparison != "" {
		query = query.Where("posts.created_at "+comparison+" ?", cutoff)
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
	result := make([]recommendation.Candidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, recommendation.Candidate{PostID: row.PostID, PositiveSemanticSimilarity: recommendation.ClampSemanticSimilarity(row.PositiveSemanticSimilarity), FromSemantic: true})
	}
	return result, nil
}

func loadRecommendationFollowingCandidates(db *gorm.DB, _ string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]recommendation.Candidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := recommendationEligibilityQuery(
		db,
		db.Table("posts").
			Select("posts.id").
			Joins("JOIN user_follows AS uf ON uf.following_id = posts.author_id AND uf.follower_id = ?", userID),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	var ids []uint
	if err := query.Order("posts.created_at DESC, posts.id DESC").Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]recommendation.Candidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, recommendation.Candidate{PostID: id, FromFollowing: true})
	}
	return result, nil
}

func loadRecommendationSourceCandidates(db *gorm.DB, _ string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, order interface{}, cap int, source string) ([]recommendation.Candidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := recommendationEligibilityQuery(db, db.Table("posts").Select("posts.id"), userID, served, now, softOnly, profile.MaterializedInteractionsReady)
	query = applyLegacyProfileInteractionExclusion(query, profile)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]recommendation.Candidate, 0, len(ids))
	for _, id := range ids {
		candidate := recommendation.Candidate{PostID: id}
		switch source {
		case "recent":
			candidate.FromRecent = true
		}
		result = append(result, candidate)
	}
	return result, nil
}

func loadRecommendationCandidateSet(db *gorm.DB, servingVersion string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool) (recommendationCandidateSet, error) {
	if db == nil {
		return recommendationCandidateSet{}, errors.New("database is not initialized")
	}
	caps := recommendationCandidateCaps(profile, cfg)
	semantic, err := loadRecommendationSemanticCandidates(db, servingVersion, userID, profile, served, now, cfg, softOnly, caps.Semantic)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	following, err := loadRecommendationFollowingCandidates(db, servingVersion, userID, profile, served, now, cfg, softOnly, caps.Following)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	recent, err := loadRecommendationSourceCandidates(db, servingVersion, userID, profile, served, now, cfg, softOnly, "posts.created_at DESC, posts.id DESC", caps.Recent, "recent")
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	trending, err := loadRecommendationTrendingCandidates(db, servingVersion, userID, profile, served, now, cfg, softOnly, caps.Trending)
	if err != nil {
		return recommendationCandidateSet{}, err
	}

	merged := recommendation.FuseCandidates(
		caps.Merged,
		recommendationFusionConfig(cfg),
		recommendation.CandidateSet{Source: recommendation.CandidateSourceSemantic, Candidates: semantic},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceFollowing, Candidates: following},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceRecent, Candidates: recent},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceTrending, Candidates: trending},
	)
	for index := range merged {
		if item, ok := served[merged[index].PostID]; ok {
			merged[index].LastServedAt = item.LastServedAt
			merged[index].WasSoftServed = softOnly && item.Soft && !item.Hard
		}
	}
	return recommendationCandidateSet{
		Candidates: merged, SemanticCount: len(semantic), FollowingCount: len(following),
		RecentCount: len(recent), RecentPostIDs: recommendation.CandidatePostIDs(recent), TrendingCount: len(trending),
	}, nil
}

func loadRecommendationTrendingCandidates(db *gorm.DB, _ string, userID uint, profile userInterestProfile, served map[uint]servedPost, now time.Time, cfg config.RecommendationConfig, softOnly bool, cap int) ([]recommendation.Candidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	cutoff := now.AddDate(0, 0, -cfg.Trending.MaxAgeDays)
	query := recommendationEligibilityQuery(
		db,
		db.Table("posts").Select("posts.id"),
		userID, served, now, softOnly, profile.MaterializedInteractionsReady,
	).Where("posts.created_at >= ?", cutoff).
		Where("posts.like_count > 0 OR posts.reply_count > 0")
	query = applyLegacyProfileInteractionExclusion(query, profile)
	order := gorm.Expr(`
(
    LN(1 + GREATEST(posts.like_count, 0))
    + ? * LN(1 + GREATEST(posts.reply_count, 0))
)
*
EXP(
    -LN(2)
    * GREATEST(EXTRACT(EPOCH FROM (? - posts.created_at)) / 3600.0, 0)
    / ?
)
DESC,
posts.created_at DESC,
posts.id DESC`, cfg.Trending.ReplyFactor, now.UTC(), cfg.Trending.HalfLifeHours)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]recommendation.Candidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, recommendation.Candidate{PostID: id, FromTrending: true})
	}
	return result, nil
}

// publicRecommendationEligibilityQuery contains viewer-independent public-post
// eligibility predicates plus caller-provided server-side post exclusions.
//
// It must not introduce authenticated-user-specific follow, interaction,
// profile, or account predicates.
func publicRecommendationEligibilityQuery(query *gorm.DB, now time.Time, excluded map[uint]struct{}) *gorm.DB {
	query = publicPostScope(query, now).
		Where("posts.reply_to_post_id IS NULL").
		Where(
			"EXISTS (SELECT 1 FROM users AS recommendation_authors " +
				"WHERE recommendation_authors.id = posts.author_id " +
				"AND recommendation_authors.deleted_at IS NULL)",
		)
	if ids := postIDList(excluded); len(ids) > 0 {
		query = query.Where("posts.id NOT IN ?", ids)
	}
	return query
}

func loadPublicRecommendationSourceCandidates(db *gorm.DB, _ string, now time.Time, cfg config.RecommendationConfig, order interface{}, cap int, source string, excluded map[uint]struct{}) ([]recommendation.Candidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	query := publicRecommendationEligibilityQuery(db.Table("posts").Select("posts.id"), now, excluded)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]recommendation.Candidate, 0, len(ids))
	for _, id := range ids {
		candidate := recommendation.Candidate{PostID: id}
		if source == "recent" {
			candidate.FromRecent = true
		}
		result = append(result, candidate)
	}
	return result, nil
}

func loadPublicRecommendationTrendingCandidates(db *gorm.DB, _ string, now time.Time, cfg config.RecommendationConfig, cap int, excluded map[uint]struct{}) ([]recommendation.Candidate, error) {
	if cap <= 0 {
		return nil, nil
	}
	cutoff := now.AddDate(0, 0, -cfg.Trending.MaxAgeDays)
	query := publicRecommendationEligibilityQuery(db.Table("posts").Select("posts.id"), now, excluded).
		Where("posts.created_at >= ?", cutoff).
		Where("posts.like_count > 0 OR posts.reply_count > 0")
	order := gorm.Expr(`
(
    LN(1 + GREATEST(posts.like_count, 0))
    + ? * LN(1 + GREATEST(posts.reply_count, 0))
)
*
EXP(
    -LN(2)
    * GREATEST(EXTRACT(EPOCH FROM (? - posts.created_at)) / 3600.0, 0)
    / ?
)
DESC,
posts.created_at DESC,
posts.id DESC`, cfg.Trending.ReplyFactor, now.UTC(), cfg.Trending.HalfLifeHours)
	var ids []uint
	if err := query.Order(order).Limit(cap).Pluck("posts.id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]recommendation.Candidate, 0, len(ids))
	for _, id := range ids {
		result = append(result, recommendation.Candidate{PostID: id, FromTrending: true})
	}
	return result, nil
}

func loadPublicRecommendationCandidateSet(db *gorm.DB, servingVersion string, now time.Time, cfg config.RecommendationConfig, excluded map[uint]struct{}) (recommendationCandidateSet, error) {
	if db == nil {
		return recommendationCandidateSet{}, errors.New("database is not initialized")
	}
	caps := cfg.Candidates.ColdStart
	recent, err := loadPublicRecommendationSourceCandidates(db, servingVersion, now, cfg, "posts.created_at DESC, posts.id DESC", caps.Recent, "recent", excluded)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	trending, err := loadPublicRecommendationTrendingCandidates(db, servingVersion, now, cfg, caps.Trending, excluded)
	if err != nil {
		return recommendationCandidateSet{}, err
	}
	merged := recommendation.FuseCandidates(
		caps.Merged,
		recommendationFusionConfig(cfg),
		recommendation.CandidateSet{Source: recommendation.CandidateSourceRecent, Candidates: recent},
		recommendation.CandidateSet{Source: recommendation.CandidateSourceTrending, Candidates: trending},
	)
	return recommendationCandidateSet{
		Candidates:  merged,
		RecentCount: len(recent), RecentPostIDs: recommendation.CandidatePostIDs(recent),
		TrendingCount: len(trending),
	}, nil
}

func recommendationCandidateCaps(profile userInterestProfile, cfg config.RecommendationConfig) config.RecommendationCandidateCaps {
	if len(profile.PositiveVector) == 0 {
		return cfg.Candidates.ColdStart
	}
	return cfg.Candidates.Personalized
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
		Candidates:     recommendation.MergeCandidates(mergedLimit, first.Candidates, second.Candidates),
		SemanticCount:  first.SemanticCount + second.SemanticCount,
		FollowingCount: first.FollowingCount + second.FollowingCount,
		RecentCount:    first.RecentCount + second.RecentCount,
		RecentPostIDs:  recentPostIDs,
		TrendingCount:  first.TrendingCount + second.TrendingCount,
	}
}

func hydrateRecommendationCandidates(db *gorm.DB, servingVersion string, candidates []recommendation.Candidate, now time.Time) ([]recommendation.RankedCandidate, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.PostID != 0 {
			ids = append(ids, candidate.PostID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	query := publicPostScope(db.Model(&models.Post{}).Select(publicPostSelectColumns).Where("posts.id IN ?", ids), now)
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
	embeddings, err := loadRecommendationPostEmbeddings(db, validPostIDs, servingVersion)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]models.Post, len(validPosts))
	for _, post := range validPosts {
		byID[post.ID] = post
	}
	result := make([]recommendation.RankedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		post, ok := byID[candidate.PostID]
		if !ok {
			continue
		}
		embedding := embeddings[candidate.PostID]
		if len(embedding) == 0 {
			embedding = nil
		}
		result = append(result, recommendation.RankedCandidate{Candidate: candidate, Post: post, Embedding: embedding})
	}
	return result, nil
}

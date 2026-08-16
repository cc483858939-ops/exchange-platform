package controllers

import (
	"errors"
	"sort"
	"strings"
	"time"

	"Go.exchange/consts"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

type recommendationInterest struct {
	Label    string
	Affinity float64
}

func topPositiveRecommendationInterests(values map[string]float64, limit int) []recommendationInterest {
	if limit <= 0 {
		return nil
	}
	byLabel := make(map[string]float64, len(values))
	for rawLabel, affinity := range values {
		label := normalizeRecommendationLabel(rawLabel)
		if label == "" || affinity <= 0 {
			continue
		}
		if current, ok := byLabel[label]; !ok || affinity > current {
			byLabel[label] = affinity
		}
	}
	interests := make([]recommendationInterest, 0, len(byLabel))
	for label, affinity := range byLabel {
		interests = append(interests, recommendationInterest{Label: label, Affinity: affinity})
	}
	sort.Slice(interests, func(i, j int) bool {
		if interests[i].Affinity != interests[j].Affinity {
			return interests[i].Affinity > interests[j].Affinity
		}
		return interests[i].Label < interests[j].Label
	})
	if len(interests) > limit {
		interests = interests[:limit]
	}
	return interests
}

func mergeRulesV3CandidateIDs(limit int, sources ...[]uint) []uint {
	if limit <= 0 {
		return nil
	}
	merged := make([]uint, 0, limit)
	seen := make(map[uint]struct{}, limit)
	for _, source := range sources {
		for _, id := range source {
			if id == 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, id)
			if len(merged) == limit {
				return merged
			}
		}
	}
	return merged
}

func scopedRulesV3CandidateQuery(query *gorm.DB, userID uint, excluded map[uint]struct{}, lookbackStart time.Time, now time.Time) *gorm.DB {
	negative := global.Db.Table("article_behaviors AS ab").
		Select("1").
		Where("ab.user_id = ? AND ab.article_id = articles.id AND ab.action = ? AND ab.last_seen_at >= ?",
			userID, eventing.RecommendationBehaviorActionNotInterested, lookbackStart)
	laterReaction := global.Db.Table("article_reaction AS ar").
		Select("1").
		Where("ar.user_id = ? AND ar.article_id = articles.id AND ar.state_changed_at > ab.last_seen_at", userID)
	negative = negative.Where("NOT EXISTS (?)", laterReaction)
	query = publicArticleScope(query, now).
		Where("NOT EXISTS (?)", negative)
	if ids := articleIDList(excluded); len(ids) > 0 {
		query = query.Where("articles.id NOT IN ?", ids)
	}
	return query
}
func loadRulesV3CategoryCandidateIDs(userID uint, profile userInterestProfile, lookbackStart time.Time, now time.Time) ([]uint, error) {
	if profile.PersonalizedSignalCount == 0 {
		return nil, nil
	}
	interests := topPositiveRecommendationInterests(profile.Categories, recommendationTopCategoryCount)
	if len(interests) == 0 {
		return nil, nil
	}

	values := make([]string, len(interests))
	args := make([]interface{}, 0, len(interests)*2)
	for index, interest := range interests {
		// Explicit PostgreSQL casts keep pgx float64 affinities out of anonymous text VALUES columns.
		values[index] = "(CAST(? AS text), CAST(? AS double precision))"
		args = append(args, interest.Label, interest.Affinity)
	}
	interestRows := global.Db.Raw("VALUES "+strings.Join(values, ", "), args...)
	ranked := scopedRulesV3CandidateQuery(
		global.Db.Table("articles").
			Select("articles.id, LOWER(BTRIM(articles.category)) AS normalized_category, interests.affinity AS affinity, articles.created_at AS created_at, ROW_NUMBER() OVER (PARTITION BY LOWER(BTRIM(articles.category)) ORDER BY articles.created_at DESC, articles.id DESC) AS category_rank").
			Joins("JOIN (?) AS interests(label, affinity) ON LOWER(BTRIM(articles.category)) = interests.label", interestRows),
		userID,
		profile.InteractedArticleIDs,
		lookbackStart,
		now,
	).Where("articles.analysis_state = ?", consts.ArticleAnalysisStateCompleted)

	var ids []uint
	if err := global.Db.Table("(?) AS ranked", ranked).
		Select("ranked.id").
		Order("ranked.category_rank ASC, ranked.affinity DESC, ranked.normalized_category ASC, ranked.created_at DESC, ranked.id DESC").
		Limit(recommendationCategoryCandidateCap).
		Pluck("ranked.id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func loadRulesV3RecentCandidateIDs(userID uint, excluded map[uint]struct{}, lookbackStart time.Time, now time.Time) ([]uint, error) {
	var ids []uint
	query := scopedRulesV3CandidateQuery(
		global.Db.Table("articles").Select("articles.id"),
		userID,
		excluded,
		lookbackStart,
		now,
	).Where("articles.analysis_state = ?", consts.ArticleAnalysisStateCompleted)
	if err := query.Order("articles.created_at DESC, articles.id DESC").
		Limit(recommendationRecentCandidateCap).
		Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func loadRulesV3PopularCandidateIDs(userID uint, excluded map[uint]struct{}, lookbackStart time.Time, now time.Time) ([]uint, error) {
	var ids []uint
	query := scopedRulesV3CandidateQuery(
		global.Db.Table("articles").Select("articles.id"),
		userID,
		excluded,
		lookbackStart,
		now,
	).Where("articles.analysis_state = ?", consts.ArticleAnalysisStateCompleted)
	if err := query.Order("articles.like_count DESC, articles.created_at DESC, articles.id DESC").
		Limit(recommendationPopularCandidateCap).
		Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func loadRulesV3FallbackCandidateIDs(userID uint, excluded map[uint]struct{}, lookbackStart time.Time, now time.Time, limit int) ([]uint, error) {
	if limit <= 0 {
		return nil, nil
	}
	var ids []uint
	query := scopedRulesV3CandidateQuery(
		global.Db.Table("articles").Select("articles.id"),
		userID,
		excluded,
		lookbackStart,
		now,
	).Where("articles.analysis_state <> ?", consts.ArticleAnalysisStateCompleted)
	if err := query.Order("articles.like_count DESC, articles.created_at DESC, articles.id DESC").
		Limit(limit).
		Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func cloneRulesV3CandidateExclusions(excluded map[uint]struct{}) map[uint]struct{} {
	if len(excluded) == 0 {
		return make(map[uint]struct{})
	}
	cloned := make(map[uint]struct{}, len(excluded))
	for id := range excluded {
		cloned[id] = struct{}{}
	}
	return cloned
}

func hydrateRulesV3CandidateArticles(ids []uint, now time.Time) ([]models.Article, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var articles []models.Article
	query := publicArticleScope(
		global.Db.Model(&models.Article{}).Select(publicArticleSelectColumns).Where("articles.id IN ?", ids),
		now,
	)
	if err := preloadArticleAuthor(query).Find(&articles).Error; err != nil {
		return nil, err
	}
	if err := ensureArticleAuthors(articles); err != nil {
		return nil, err
	}
	byID := make(map[uint]models.Article, len(articles))
	for _, article := range articles {
		byID[article.ID] = article
	}
	ordered := make([]models.Article, 0, len(ids))
	for _, id := range ids {
		if article, ok := byID[id]; ok {
			ordered = append(ordered, article)
		}
	}
	return ordered, nil
}

var loadRulesV3Candidates = func(userID uint, profile userInterestProfile, lookbackStart, now time.Time, requestedLimit int) ([]models.Article, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	if requestedLimit <= 0 {
		requestedLimit = defaultRecommendationLimit
	}
	if requestedLimit > recommendationMergedCandidateCap {
		requestedLimit = recommendationMergedCandidateCap
	}

	categoryIDs, err := loadRulesV3CategoryCandidateIDs(userID, profile, lookbackStart, now)
	if err != nil {
		return nil, err
	}
	recentIDs, err := loadRulesV3RecentCandidateIDs(userID, profile.InteractedArticleIDs, lookbackStart, now)
	if err != nil {
		return nil, err
	}
	popularIDs, err := loadRulesV3PopularCandidateIDs(userID, profile.InteractedArticleIDs, lookbackStart, now)
	if err != nil {
		return nil, err
	}
	completedIDs := mergeRulesV3CandidateIDs(recommendationMergedCandidateCap, categoryIDs, recentIDs, popularIDs)
	allIDs := completedIDs
	if len(completedIDs) < requestedLimit {
		fallbackExcluded := cloneRulesV3CandidateExclusions(profile.InteractedArticleIDs)
		for _, id := range completedIDs {
			fallbackExcluded[id] = struct{}{}
		}
		fallbackIDs, err := loadRulesV3FallbackCandidateIDs(userID, fallbackExcluded, lookbackStart, now, requestedLimit-len(completedIDs))
		if err != nil {
			return nil, err
		}
		allIDs = mergeRulesV3CandidateIDs(recommendationMergedCandidateCap, completedIDs, fallbackIDs)
	}
	return hydrateRulesV3CandidateArticles(allIDs, now)
}

package controllers

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	recommendationFeedbackEventLimit     = 500
	recommendationFeedbackSignalCountCap = 5
	recommendationBehaviorCountCap       = 5
)

type recommendationFeedbackSignal struct {
	Event      models.RecommendationEvent
	SignalType string
	Article    *models.Article
}

func normalizedRulesV2RecommendationConfig() config.RecommendationConfig {
	cfg := config.RecommendationConfig{
		BehaviorWeights:    config.RecommendationBehaviorWeights{View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, QuickBounce: 3, NotInterested: 6},
		SignalHalfLifeDays: 14, FeedbackLookbackDays: 90, InterestSaturationScale: 6,
		CategoryWeight: 3, TagWeight: 2, PopularityWeight: 0.5, FreshnessWeight: 1,
	}
	if config.AppConfig == nil {
		return cfg
	}
	set := config.AppConfig.Recommendation
	if set.BehaviorWeights.View > 0 {
		cfg.BehaviorWeights.View = set.BehaviorWeights.View
	}
	if set.BehaviorWeights.Like > 0 {
		cfg.BehaviorWeights.Like = set.BehaviorWeights.Like
	}
	if set.BehaviorWeights.Click > 0 {
		cfg.BehaviorWeights.Click = set.BehaviorWeights.Click
	}
	if set.BehaviorWeights.QualifiedRead > 0 {
		cfg.BehaviorWeights.QualifiedRead = set.BehaviorWeights.QualifiedRead
	}
	if set.BehaviorWeights.QuickBounce > 0 {
		cfg.BehaviorWeights.QuickBounce = set.BehaviorWeights.QuickBounce
	}
	if set.BehaviorWeights.NotInterested > 0 {
		cfg.BehaviorWeights.NotInterested = set.BehaviorWeights.NotInterested
	}
	if set.SignalHalfLifeDays > 0 {
		cfg.SignalHalfLifeDays = set.SignalHalfLifeDays
	}
	if set.FeedbackLookbackDays > 0 {
		cfg.FeedbackLookbackDays = set.FeedbackLookbackDays
	}
	if set.InterestSaturationScale > 0 {
		cfg.InterestSaturationScale = set.InterestSaturationScale
	}
	if set.CategoryWeight > 0 {
		cfg.CategoryWeight = set.CategoryWeight
	}
	if set.TagWeight > 0 {
		cfg.TagWeight = set.TagWeight
	}
	if set.PopularityWeight > 0 {
		cfg.PopularityWeight = set.PopularityWeight
	}
	if set.FreshnessWeight > 0 {
		cfg.FreshnessWeight = set.FreshnessWeight
	}
	return cfg
}

var loadRecommendationFeedbackSignals = func(userID uint, lookbackStart time.Time) ([]recommendationFeedbackSignal, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var events []models.RecommendationEvent
	if err := global.Db.Where("user_id = ? AND occurred_at >= ? AND event_type IN ?", userID, lookbackStart, []string{models.RecommendationEventTypeClick, models.RecommendationEventTypeReadEnd, models.RecommendationEventTypeNotInterested}).Order("occurred_at DESC, received_at DESC, event_id DESC").Limit(recommendationFeedbackEventLimit).Find(&events).Error; err != nil {
		return nil, err
	}
	kept := make([]models.RecommendationEvent, 0, len(events))
	counts := map[string]int{}
	ids := map[uint]struct{}{}
	for _, event := range events {
		signal := normalizeRecommendationFeedbackSignal(event)
		key := strconvUint(event.ArticleID) + ":" + signal
		if counts[key] >= recommendationFeedbackSignalCountCap {
			continue
		}
		counts[key]++
		kept = append(kept, event)
		if event.ArticleID != 0 {
			ids[event.ArticleID] = struct{}{}
		}
	}
	articles := map[uint]models.Article{}
	if len(ids) > 0 {
		var loaded []models.Article
		if err := global.Db.Select("id,category,tags").Where("id IN ?", articleIDList(ids)).Find(&loaded).Error; err != nil {
			return nil, err
		}
		for _, a := range loaded {
			articles[a.ID] = a
		}
	}
	result := make([]recommendationFeedbackSignal, 0, len(kept))
	for _, event := range kept {
		signal := recommendationFeedbackSignal{Event: event, SignalType: normalizeRecommendationFeedbackSignal(event)}
		if article, ok := articles[event.ArticleID]; ok {
			signal.Article = &article
		}
		result = append(result, signal)
	}
	return result, nil
}

func normalizeRecommendationFeedbackSignal(event models.RecommendationEvent) string {
	switch event.EventType {
	case models.RecommendationEventTypeClick:
		return "click"
	case models.RecommendationEventTypeNotInterested:
		return "not_interested"
	case models.RecommendationEventTypeReadEnd:
		if event.QualifiedRead {
			return "qualified_read"
		}
		if event.QuickBounce {
			return "quick_bounce"
		}
		return "neutral_read_end"
	default:
		return "impression"
	}
}

func buildRulesV2InterestProfile(behaviors []articleBehaviorSignal, feedback []recommendationFeedbackSignal, now time.Time, cfg config.RecommendationConfig) userInterestProfile {
	profile := userInterestProfile{Categories: map[string]float64{}, Tags: map[string]float64{}, InteractedArticleIDs: map[uint]struct{}{}}
	rawCategories, rawTags := map[string]float64{}, map[string]float64{}
	add := func(article *models.Article, articleID uint, signal string, count int64, occurred time.Time) {
		if articleID != 0 && signal != "impression" {
			profile.InteractedArticleIDs[articleID] = struct{}{}
		}
		weight := rulesV2SignalWeight(cfg, signal)
		if weight == 0 || article == nil {
			return
		}
		factor := float64(count)
		if factor < 1 {
			factor = 1
		}
		if factor > recommendationBehaviorCountCap {
			factor = recommendationBehaviorCountCap
		}
		effective := weight * factor * rulesV2Decay(now, occurred, cfg.SignalHalfLifeDays)
		accepted := false
		if category := normalizeRecommendationLabel(article.Category); category != "" {
			rawCategories[category] += effective
			accepted = true
		}
		for _, tag := range uniqueRecommendationTags(article.Tags) {
			rawTags[tag] += effective
			accepted = true
		}
		if accepted {
			profile.PersonalizedSignalCount++
		}
	}
	for _, item := range behaviors {
		if !item.Behavior.Active {
			continue
		}
		signal := ""
		if item.Behavior.Action == ArticleBehaviorActionView {
			signal = "view"
		} else if item.Behavior.Action == ArticleBehaviorActionLike {
			signal = "like"
		}
		add(&item.Article, item.Behavior.ArticleID, signal, item.Behavior.Count, item.Behavior.LastSeenAt)
	}
	for _, item := range feedback {
		add(item.Article, item.Event.ArticleID, item.SignalType, 1, item.Event.OccurredAt)
	}
	for key, value := range rawCategories {
		profile.Categories[key] = math.Tanh(value / cfg.InterestSaturationScale)
	}
	for key, value := range rawTags {
		profile.Tags[key] = math.Tanh(value / cfg.InterestSaturationScale)
	}
	return profile
}

func rulesV2SignalWeight(cfg config.RecommendationConfig, signal string) float64 {
	switch signal {
	case "view":
		return cfg.BehaviorWeights.View
	case "like":
		return cfg.BehaviorWeights.Like
	case "click":
		return cfg.BehaviorWeights.Click
	case "qualified_read":
		return cfg.BehaviorWeights.QualifiedRead
	case "quick_bounce":
		return -cfg.BehaviorWeights.QuickBounce
	case "not_interested":
		return -cfg.BehaviorWeights.NotInterested
	default:
		return 0
	}
}
func rulesV2Decay(now, occurred time.Time, halfLifeDays float64) float64 {
	if occurred.IsZero() || occurred.After(now) || halfLifeDays <= 0 {
		return 1
	}
	age := now.Sub(occurred).Hours() / 24
	return math.Exp(-math.Ln2 * age / halfLifeDays)
}
func uniqueRecommendationTags(tags []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		normalized := normalizeRecommendationLabel(tag)
		if normalized != "" {
			if _, ok := seen[normalized]; !ok {
				seen[normalized] = struct{}{}
				result = append(result, normalized)
			}
		}
	}
	return result
}
func strconvUint(id uint) string { return strconv.FormatUint(uint64(id), 10) }

var loadRulesV2Candidates = func(userID uint, excluded map[uint]struct{}, lookbackStart, now time.Time) ([]models.Article, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	columns := "id,title,content,preview,summary,cover_image_url,tags,category,publication_state,analysis_state,like_count,comment_count,created_at,updated_at,deleted_at,author_id"
	scoped := func(query *gorm.DB) *gorm.DB {
		sub := global.Db.Table("recommendation_events AS re").Select("1").Where("re.user_id = ? AND re.article_id = articles.id AND re.event_type = ? AND re.occurred_at >= ?", userID, models.RecommendationEventTypeNotInterested, lookbackStart)
		query = query.Where("publication_state = ?", consts.ArticlePublicationStatePublished).Where("expired_at > ? OR expired_at IS NULL", now).Where("NOT EXISTS (?)", sub)
		if ids := articleIDList(excluded); len(ids) > 0 {
			query = query.Where("id NOT IN ?", ids)
		}
		return query
	}
	var completed []models.Article
	if err := preloadArticleAuthor(scoped(global.Db.Select(columns))).Where("analysis_state = ?", consts.ArticleAnalysisStateCompleted).Order("created_at DESC, id DESC").Limit(recommendationCandidateCap).Find(&completed).Error; err != nil {
		return nil, err
	}
	if len(completed) >= recommendationCandidateCap {
		if err := ensureArticleAuthors(completed); err != nil {
			return nil, err
		}
		return completed, nil
	}
	excludedIDs := append(articleIDList(excluded), articleIDs(completed)...)
	query := preloadArticleAuthor(scoped(global.Db.Select(columns))).Where("analysis_state <> ?", consts.ArticleAnalysisStateCompleted)
	if len(excludedIDs) > 0 {
		query = query.Where("id NOT IN ?", excludedIDs)
	}
	var fallback []models.Article
	if err := query.Order("like_count DESC, created_at DESC, id DESC").Limit(recommendationCandidateCap - len(completed)).Find(&fallback).Error; err != nil {
		return nil, err
	}
	candidates := append(completed, fallback...)
	if err := ensureArticleAuthors(candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

func recommendRulesV2Articles(profile userInterestProfile, candidates []models.Article, now time.Time, cfg config.RecommendationConfig, limit int) []recommendedArticleResponse {
	result := make([]recommendedArticleResponse, 0, len(candidates))
	for _, article := range candidates {
		if _, ok := profile.InteractedArticleIDs[article.ID]; ok {
			continue
		}
		result = append(result, recommendedArticleResponse{ID: article.ID, Title: article.Title, Content: article.Content, Preview: article.Preview, Summary: article.Summary, CoverImageURL: article.CoverImageURL, Tags: recommendationTags(article.Tags), Category: article.Category, LikeCount: article.LikeCount, CommentCount: article.CommentCount, CreatedAt: article.CreatedAt, Author: publicAuthorResponse{ID: article.Author.ID, Username: article.Author.Username}, Score: scoreRulesV2Article(profile, article, now, cfg)})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if !result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].CreatedAt.After(result[j].CreatedAt)
		}
		return result[i].ID > result[j].ID
	})
	if limit > maxRecommendationLimit {
		limit = maxRecommendationLimit
	}
	if limit <= 0 {
		limit = defaultRecommendationLimit
	}
	if len(result) > limit {
		return result[:limit]
	}
	return result
}
func scoreRulesV2Article(profile userInterestProfile, article models.Article, now time.Time, cfg config.RecommendationConfig) float64 {
	tagTotal := 0.0
	tags := uniqueRecommendationTags(article.Tags)
	for _, tag := range tags {
		tagTotal += profile.Tags[tag]
	}
	tagMatch := 0.0
	if len(tags) > 0 {
		tagMatch = tagTotal / float64(len(tags))
	}
	score := profile.Categories[normalizeRecommendationLabel(article.Category)]*cfg.CategoryWeight + tagMatch*cfg.TagWeight + math.Log(float64(article.LikeCount)+1)*cfg.PopularityWeight + freshnessScore(article.CreatedAt, now)*cfg.FreshnessWeight
	if article.AnalysisState == consts.ArticleAnalysisStateCompleted {
		score += 1000
	}
	return score
}

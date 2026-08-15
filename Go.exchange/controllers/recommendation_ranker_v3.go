package controllers

import (
	"errors"
	"math"
	"sort"
	"time"

	"Go.exchange/config"
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"

	"gorm.io/gorm"
)

const (
	recommendationFeedbackArticleLimit   = 500
	recommendationRecentViewArticleLimit = 200
)

type recommendationFeedbackSignal struct {
	Event      models.RecommendationEvent
	SignalType string
	Article    *models.Article
}

type recommendationReactionState struct {
	Liked          bool
	StateChangedAt time.Time
	Article        *models.Article
}

type userArticleOutcome struct {
	ArticleID  uint
	Article    *models.Article
	SignalType string
	OccurredAt time.Time
}

func normalizedRulesV3RecommendationConfig() config.RecommendationConfig {
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

	query := "WITH ranked AS (\n" +
		"  SELECT re.*, ROW_NUMBER() OVER (\n" +
		"    PARTITION BY re.article_id, re.event_type\n" +
		"    ORDER BY re.occurred_at DESC, re.received_at DESC, re.event_id DESC\n" +
		"  ) AS type_rank\n" +
		"  FROM recommendation_events AS re\n" +
		"  WHERE re.user_id = ? AND re.occurred_at >= ?\n" +
		"    AND re.event_type IN ('click', 'read_end', 'not_interested')\n" +
		"), latest_per_type AS (\n" +
		"  SELECT * FROM ranked WHERE type_rank = 1\n" +
		"), selected_articles AS (\n" +
		"  SELECT article_id, MAX(occurred_at) AS latest_occurred_at\n" +
		"  FROM latest_per_type\n" +
		"  GROUP BY article_id\n" +
		"  ORDER BY MAX(occurred_at) DESC, article_id DESC\n" +
		"  LIMIT ?\n" +
		")\n" +
		"SELECT lpt.*\n" +
		"FROM latest_per_type AS lpt\n" +
		"JOIN selected_articles AS sa ON sa.article_id = lpt.article_id\n" +
		"ORDER BY sa.latest_occurred_at DESC, lpt.occurred_at DESC, lpt.received_at DESC, lpt.event_id DESC"

	var events []models.RecommendationEvent
	if err := global.Db.Raw(query, userID, lookbackStart, recommendationFeedbackArticleLimit).Scan(&events).Error; err != nil {
		return nil, err
	}

	articleIDsByEvent := make(map[uint]struct{}, len(events))
	for _, event := range events {
		if event.ArticleID != 0 {
			articleIDsByEvent[event.ArticleID] = struct{}{}
		}
	}
	articles := make(map[uint]models.Article, len(articleIDsByEvent))
	if len(articleIDsByEvent) > 0 {
		var loaded []models.Article
		if err := global.Db.Select("id,category,tags").Where("id IN ?", articleIDList(articleIDsByEvent)).Find(&loaded).Error; err != nil {
			return nil, err
		}
		for _, article := range loaded {
			articles[article.ID] = article
		}
	}

	result := make([]recommendationFeedbackSignal, 0, len(events))
	for _, event := range events {
		signal := recommendationFeedbackSignal{
			Event:      event,
			SignalType: normalizeRecommendationFeedbackSignal(event),
		}
		if article, ok := articles[event.ArticleID]; ok {
			articleCopy := article
			signal.Article = &articleCopy
		}
		result = append(result, signal)
	}
	return result, nil
}

var loadRecommendationReactionStates = func(userID uint) (map[uint]recommendationReactionState, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var reactions []models.ArticleReaction
	if err := global.Db.Where("user_id = ?", userID).
		Order("article_id ASC").
		Find(&reactions).Error; err != nil {
		return nil, err
	}

	articleIDs := make([]uint, 0, len(reactions))
	seenArticleIDs := make(map[uint]struct{}, len(reactions))
	for _, reaction := range reactions {
		if reaction.ArticleID == 0 {
			continue
		}
		if _, exists := seenArticleIDs[reaction.ArticleID]; exists {
			continue
		}
		seenArticleIDs[reaction.ArticleID] = struct{}{}
		articleIDs = append(articleIDs, reaction.ArticleID)
	}
	articles := make(map[uint]models.Article, len(articleIDs))
	if len(articleIDs) > 0 {
		var loaded []models.Article
		if err := global.Db.Select("id,tags,category").Where("id IN ?", articleIDs).Find(&loaded).Error; err != nil {
			return nil, err
		}
		for _, article := range loaded {
			articles[article.ID] = article
		}
	}

	states := make(map[uint]recommendationReactionState, len(reactions))
	for _, reaction := range reactions {
		if reaction.ArticleID == 0 {
			continue
		}
		state := recommendationReactionState{
			Liked:          reaction.Liked,
			StateChangedAt: reaction.StateChangedAt,
		}
		if article, ok := articles[reaction.ArticleID]; ok {
			articleCopy := article
			state.Article = &articleCopy
		}
		states[reaction.ArticleID] = state
	}
	return states, nil
}

func normalizeRecommendationFeedbackSignal(event models.RecommendationEvent) string {
	switch event.EventType {
	case models.RecommendationEventTypeClick:
		return "click"
	case models.RecommendationEventTypeNotInterested:
		return "not_interested"
	case models.RecommendationEventTypeReadEnd:
		if event.ReadOutcome == nil {
			return "neutral_read"
		}
		switch *event.ReadOutcome {
		case recommendationReadOutcomeQualified:
			return "qualified_read"
		case recommendationReadOutcomeQuickBounce:
			return "quick_bounce"
		default:
			return "neutral_read"
		}
	default:
		return ""
	}
}

func recommendationEventAfter(candidate, current models.RecommendationEvent) bool {
	if !candidate.OccurredAt.Equal(current.OccurredAt) {
		return candidate.OccurredAt.After(current.OccurredAt)
	}
	if !candidate.ReceivedAt.Equal(current.ReceivedAt) {
		return candidate.ReceivedAt.After(current.ReceivedAt)
	}
	return candidate.EventID > current.EventID
}

func setLatestRecommendationEvent(target **models.RecommendationEvent, candidate models.RecommendationEvent) {
	if *target == nil || recommendationEventAfter(candidate, **target) {
		candidateCopy := candidate
		*target = &candidateCopy
	}
}

type recommendationArticleFeedbackState struct {
	Click         *models.RecommendationEvent
	ReadEnd       *models.RecommendationEvent
	NotInterested *models.RecommendationEvent
}

func canonicalizeRecommendationOutcomes(
	behaviors []articleBehaviorSignal,
	feedback []recommendationFeedbackSignal,
	reactions map[uint]recommendationReactionState,
) []userArticleOutcome {
	articleByID := make(map[uint]*models.Article)
	views := make(map[uint]models.ArticleBehavior)
	feedbackByArticle := make(map[uint]*recommendationArticleFeedbackState)
	articleIDs := make(map[uint]struct{})

	for _, item := range behaviors {
		if item.Behavior.Action != ArticleBehaviorActionView {
			continue
		}
		articleID := item.Behavior.ArticleID
		if articleID == 0 {
			continue
		}
		articleIDs[articleID] = struct{}{}
		articleCopy := item.Article
		if _, exists := articleByID[articleID]; !exists {
			articleByID[articleID] = &articleCopy
		}
		current, exists := views[articleID]
		if !exists || item.Behavior.LastSeenAt.After(current.LastSeenAt) ||
			(item.Behavior.LastSeenAt.Equal(current.LastSeenAt) && item.Behavior.ID > current.ID) {
			views[articleID] = item.Behavior
		}
	}

	for _, item := range feedback {
		articleID := item.Event.ArticleID
		if articleID == 0 {
			continue
		}
		articleIDs[articleID] = struct{}{}
		if item.Article != nil {
			articleCopy := *item.Article
			articleByID[articleID] = &articleCopy
		}
		state := feedbackByArticle[articleID]
		if state == nil {
			state = &recommendationArticleFeedbackState{}
			feedbackByArticle[articleID] = state
		}
		switch item.Event.EventType {
		case models.RecommendationEventTypeClick:
			setLatestRecommendationEvent(&state.Click, item.Event)
		case models.RecommendationEventTypeReadEnd:
			setLatestRecommendationEvent(&state.ReadEnd, item.Event)
		case models.RecommendationEventTypeNotInterested:
			setLatestRecommendationEvent(&state.NotInterested, item.Event)
		}
	}

	for articleID, reaction := range reactions {
		if articleID == 0 {
			continue
		}
		articleIDs[articleID] = struct{}{}
		if reaction.Article != nil {
			articleCopy := *reaction.Article
			articleByID[articleID] = &articleCopy
		}
	}

	outcomes := make([]userArticleOutcome, 0, len(articleIDs))
	for articleID := range articleIDs {
		state := feedbackByArticle[articleID]
		reaction, hasReaction := reactions[articleID]
		var notInterested *models.RecommendationEvent
		if state != nil {
			notInterested = state.NotInterested
		}

		signalType := ""
		occurredAt := time.Time{}
		switch {
		case notInterested != nil && (!hasReaction || !reaction.StateChangedAt.After(notInterested.OccurredAt)):
			signalType = "not_interested"
			occurredAt = notInterested.OccurredAt
		case notInterested != nil && hasReaction && reaction.StateChangedAt.After(notInterested.OccurredAt) && reaction.Liked:
			signalType = "like"
			occurredAt = reaction.StateChangedAt
		case notInterested == nil && hasReaction && reaction.Liked:
			signalType = "like"
			occurredAt = reaction.StateChangedAt
		case state != nil && state.ReadEnd != nil:
			signalType = normalizeRecommendationFeedbackSignal(*state.ReadEnd)
			occurredAt = state.ReadEnd.OccurredAt
		case state != nil && state.Click != nil:
			signalType = "click"
			occurredAt = state.Click.OccurredAt
		case views[articleID].ArticleID != 0:
			signalType = "view"
			occurredAt = views[articleID].LastSeenAt
		}
		if signalType == "" {
			continue
		}
		outcomes = append(outcomes, userArticleOutcome{
			ArticleID: articleID, Article: articleByID[articleID],
			SignalType: signalType, OccurredAt: occurredAt,
		})
	}
	sort.Slice(outcomes, func(i, j int) bool {
		return outcomes[i].ArticleID < outcomes[j].ArticleID
	})
	return outcomes
}

func buildRulesV3InterestProfile(
	behaviors []articleBehaviorSignal,
	feedback []recommendationFeedbackSignal,
	reactions map[uint]recommendationReactionState,
	now time.Time,
	cfg config.RecommendationConfig,
) userInterestProfile {
	profile := userInterestProfile{
		Categories:           map[string]float64{},
		Tags:                 map[string]float64{},
		InteractedArticleIDs: map[uint]struct{}{},
	}
	for articleID := range reactions {
		if articleID != 0 {
			profile.InteractedArticleIDs[articleID] = struct{}{}
		}
	}
	rawCategories, rawTags := map[string]float64{}, map[string]float64{}
	add := func(outcome userArticleOutcome) {
		if outcome.ArticleID != 0 {
			profile.InteractedArticleIDs[outcome.ArticleID] = struct{}{}
		}
		weight := rulesV3SignalWeight(cfg, outcome.SignalType)
		if weight == 0 || outcome.Article == nil {
			return
		}
		effective := weight * rulesV3Decay(now, outcome.OccurredAt, cfg.SignalHalfLifeDays)
		accepted := false
		if category := normalizeRecommendationLabel(outcome.Article.Category); category != "" {
			rawCategories[category] += effective
			accepted = true
		}
		for _, tag := range uniqueRecommendationTags(outcome.Article.Tags) {
			rawTags[tag] += effective
			accepted = true
		}
		if accepted {
			profile.PersonalizedSignalCount++
		}
	}
	for _, outcome := range canonicalizeRecommendationOutcomes(behaviors, feedback, reactions) {
		add(outcome)
	}
	for key, value := range rawCategories {
		profile.Categories[key] = math.Tanh(value / cfg.InterestSaturationScale)
	}
	for key, value := range rawTags {
		profile.Tags[key] = math.Tanh(value / cfg.InterestSaturationScale)
	}
	return profile
}

func rulesV3SignalWeight(cfg config.RecommendationConfig, signal string) float64 {
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

func rulesV3Decay(now, occurred time.Time, halfLifeDays float64) float64 {
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

var loadRulesV3Candidates = func(userID uint, excluded map[uint]struct{}, lookbackStart, now time.Time) ([]models.Article, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	columns := "id,title,content,preview,summary,cover_image_url,tags,category,publication_state,analysis_state,like_count,comment_count,created_at,updated_at,deleted_at,author_id"
	scoped := func(query *gorm.DB) *gorm.DB {
		laterReaction := global.Db.Table("article_reaction AS ar").
			Select("1").
			Where("ar.user_id = ? AND ar.article_id = articles.id AND ar.state_changed_at > re.occurred_at", userID)
		negative := global.Db.Table("recommendation_events AS re").
			Select("1").
			Where("re.user_id = ? AND re.article_id = articles.id AND re.event_type = ? AND re.occurred_at >= ?", userID, models.RecommendationEventTypeNotInterested, lookbackStart).
			Where("NOT EXISTS (?)", laterReaction)
		query = query.Where("publication_state = ?", consts.ArticlePublicationStatePublished).
			Where("expired_at > ? OR expired_at IS NULL", now).
			Where("NOT EXISTS (?)", negative)
		if ids := articleIDList(excluded); len(ids) > 0 {
			query = query.Where("id NOT IN ?", ids)
		}
		return query
	}
	var completed []models.Article
	if err := preloadArticleAuthor(scoped(global.Db.Select(columns))).
		Where("analysis_state = ?", consts.ArticleAnalysisStateCompleted).
		Order("created_at DESC, id DESC").
		Limit(recommendationCandidateCap).
		Find(&completed).Error; err != nil {
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
	if err := query.Order("like_count DESC, created_at DESC, id DESC").
		Limit(recommendationCandidateCap - len(completed)).
		Find(&fallback).Error; err != nil {
		return nil, err
	}
	candidates := append(completed, fallback...)
	if err := ensureArticleAuthors(candidates); err != nil {
		return nil, err
	}
	return candidates, nil
}

func recommendRulesV3Articles(profile userInterestProfile, candidates []models.Article, now time.Time, cfg config.RecommendationConfig, limit int) []recommendedArticleResponse {
	result := make([]recommendedArticleResponse, 0, len(candidates))
	for _, article := range candidates {
		if _, ok := profile.InteractedArticleIDs[article.ID]; ok {
			continue
		}
		result = append(result, recommendedArticleResponse{
			ID: article.ID, Title: article.Title, Content: article.Content, Preview: article.Preview,
			Summary: article.Summary, CoverImageURL: article.CoverImageURL, Tags: recommendationTags(article.Tags),
			Category: article.Category, LikeCount: article.LikeCount, CommentCount: article.CommentCount,
			CreatedAt: article.CreatedAt, Author: publicAuthorFromUser(article.Author),
			Score: scoreRulesV3Article(profile, article, now, cfg),
		})
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

func scoreRulesV3Article(profile userInterestProfile, article models.Article, now time.Time, cfg config.RecommendationConfig) float64 {
	tagTotal := 0.0
	tags := uniqueRecommendationTags(article.Tags)
	for _, tag := range tags {
		tagTotal += profile.Tags[tag]
	}
	tagMatch := 0.0
	if len(tags) > 0 {
		tagMatch = tagTotal / float64(len(tags))
	}
	score := profile.Categories[normalizeRecommendationLabel(article.Category)]*cfg.CategoryWeight +
		tagMatch*cfg.TagWeight +
		math.Log(float64(article.LikeCount)+1)*cfg.PopularityWeight +
		freshnessScore(article.CreatedAt, now)*cfg.FreshnessWeight
	if article.AnalysisState == consts.ArticleAnalysisStateCompleted {
		score += 1000
	}
	return score
}

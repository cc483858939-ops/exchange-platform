package controllers

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"Go.exchange/config"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

type recommendationFeedbackEvent struct {
	EventID     string
	ArticleID   uint
	EventType   string
	OccurredAt  time.Time
	ReceivedAt  time.Time
	ReadOutcome *string
}

type recommendationFeedbackSignal struct {
	Event      recommendationFeedbackEvent
	SignalType string
}

type recommendationReactionState struct {
	Liked          bool
	StateChangedAt time.Time
}

type userArticleOutcome struct {
	ArticleID  uint
	SignalType string
	OccurredAt time.Time
}

const (
	recommendationFeedbackEventTypeClick         = models.RecommendationEventTypeClick
	recommendationFeedbackEventTypeReadEnd       = models.RecommendationEventTypeReadEnd
	recommendationFeedbackEventTypeNotInterested = models.RecommendationEventTypeNotInterested
)

var loadRecommendationFeedbackSignals = func(userID uint, lookbackStart time.Time) ([]recommendationFeedbackSignal, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	actions := []string{
		eventing.RecommendationBehaviorActionClick,
		eventing.RecommendationBehaviorActionReadQualified,
		eventing.RecommendationBehaviorActionReadQuickBounce,
		eventing.RecommendationBehaviorActionReadNeutral,
		eventing.RecommendationBehaviorActionNotInterested,
	}
	var behaviors []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND action IN ? AND last_seen_at >= ?", userID, actions, lookbackStart).
		Order("last_seen_at DESC, id DESC").
		Limit(recommendationFeedbackArticleLimit * len(actions)).
		Find(&behaviors).Error; err != nil {
		return nil, err
	}
	selected := make(map[uint]struct{}, recommendationFeedbackArticleLimit)
	result := make([]recommendationFeedbackSignal, 0, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ArticleID == 0 {
			continue
		}
		if _, ok := selected[behavior.ArticleID]; !ok {
			if len(selected) >= recommendationFeedbackArticleLimit {
				continue
			}
			selected[behavior.ArticleID] = struct{}{}
		}
		event := recommendationFeedbackEvent{
			EventID: strconv.FormatUint(uint64(behavior.ID), 10), ArticleID: behavior.ArticleID,
			OccurredAt: behavior.LastSeenAt, ReceivedAt: behavior.UpdatedAt,
		}
		if event.ReceivedAt.IsZero() {
			event.ReceivedAt = event.OccurredAt
		}
		signal := recommendationFeedbackSignal{Event: event}
		switch behavior.Action {
		case eventing.RecommendationBehaviorActionClick:
			event.EventType = recommendationFeedbackEventTypeClick
			signal.SignalType = "click"
		case eventing.RecommendationBehaviorActionReadQualified,
			eventing.RecommendationBehaviorActionReadQuickBounce,
			eventing.RecommendationBehaviorActionReadNeutral:
			event.EventType = recommendationFeedbackEventTypeReadEnd
			outcome := recommendationReadOutcomeNeutral
			switch behavior.Action {
			case eventing.RecommendationBehaviorActionReadQualified:
				outcome = recommendationReadOutcomeQualified
			case eventing.RecommendationBehaviorActionReadQuickBounce:
				outcome = recommendationReadOutcomeQuickBounce
			}
			event.ReadOutcome = &outcome
			signal.SignalType = normalizeRecommendationFeedbackSignal(event)
		case eventing.RecommendationBehaviorActionNotInterested:
			event.EventType = recommendationFeedbackEventTypeNotInterested
			signal.SignalType = "not_interested"
		default:
			continue
		}
		signal.Event = event
		result = append(result, signal)
	}
	return result, nil
}

var loadRecommendationReactionStates = func(userID uint) (map[uint]recommendationReactionState, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var reactions []models.ArticleReaction
	if err := global.Db.Where("user_id = ?", userID).Order("article_id ASC").Find(&reactions).Error; err != nil {
		return nil, err
	}
	states := make(map[uint]recommendationReactionState, len(reactions))
	for _, reaction := range reactions {
		if reaction.ArticleID == 0 {
			continue
		}
		states[reaction.ArticleID] = recommendationReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
	}
	return states, nil
}

func normalizeRecommendationFeedbackSignal(event recommendationFeedbackEvent) string {
	switch event.EventType {
	case recommendationFeedbackEventTypeClick:
		return "click"
	case recommendationFeedbackEventTypeNotInterested:
		return "not_interested"
	case recommendationFeedbackEventTypeReadEnd:
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

func recommendationEventAfter(candidate, current recommendationFeedbackEvent) bool {
	if !candidate.OccurredAt.Equal(current.OccurredAt) {
		return candidate.OccurredAt.After(current.OccurredAt)
	}
	if !candidate.ReceivedAt.Equal(current.ReceivedAt) {
		return candidate.ReceivedAt.After(current.ReceivedAt)
	}
	return candidate.EventID > current.EventID
}

func setLatestRecommendationEvent(target **recommendationFeedbackEvent, candidate recommendationFeedbackEvent) {
	if *target == nil || recommendationEventAfter(candidate, **target) {
		candidateCopy := candidate
		*target = &candidateCopy
	}
}

type recommendationArticleFeedbackState struct {
	Click         *recommendationFeedbackEvent
	ReadEnd       *recommendationFeedbackEvent
	NotInterested *recommendationFeedbackEvent
}

func resolveRecommendationPassiveOutcome(state *recommendationArticleFeedbackState, view models.ArticleBehavior) (string, time.Time) {
	var click, readEnd *recommendationFeedbackEvent
	if state != nil {
		click = state.Click
		readEnd = state.ReadEnd
	}
	if readEnd != nil {
		if click != nil && click.OccurredAt.After(readEnd.OccurredAt) {
			return "click", click.OccurredAt
		}
		if view.ArticleID != 0 && view.LastSeenAt.After(readEnd.OccurredAt) {
			return "view", view.LastSeenAt
		}
		return normalizeRecommendationFeedbackSignal(*readEnd), readEnd.OccurredAt
	}
	if click != nil {
		return "click", click.OccurredAt
	}
	if view.ArticleID != 0 {
		return "view", view.LastSeenAt
	}
	return "", time.Time{}
}

func canonicalizeRecommendationOutcomes(behaviors []articleBehaviorSignal, feedback []recommendationFeedbackSignal, reactions map[uint]recommendationReactionState) []userArticleOutcome {
	views := make(map[uint]models.ArticleBehavior)
	feedbackByArticle := make(map[uint]*recommendationArticleFeedbackState)
	articleIDs := make(map[uint]struct{})
	for _, item := range behaviors {
		if item.Behavior.Action != ArticleBehaviorActionView || item.Behavior.ArticleID == 0 {
			continue
		}
		articleID := item.Behavior.ArticleID
		articleIDs[articleID] = struct{}{}
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
		state := feedbackByArticle[articleID]
		if state == nil {
			state = &recommendationArticleFeedbackState{}
			feedbackByArticle[articleID] = state
		}
		switch item.Event.EventType {
		case recommendationFeedbackEventTypeClick:
			setLatestRecommendationEvent(&state.Click, item.Event)
		case recommendationFeedbackEventTypeReadEnd:
			setLatestRecommendationEvent(&state.ReadEnd, item.Event)
		case recommendationFeedbackEventTypeNotInterested:
			setLatestRecommendationEvent(&state.NotInterested, item.Event)
		}
	}
	for articleID := range reactions {
		if articleID != 0 {
			articleIDs[articleID] = struct{}{}
		}
	}
	outcomes := make([]userArticleOutcome, 0, len(articleIDs))
	for articleID := range articleIDs {
		state := feedbackByArticle[articleID]
		reaction, hasReaction := reactions[articleID]
		var notInterested *recommendationFeedbackEvent
		if state != nil {
			notInterested = state.NotInterested
		}
		signalType := ""
		occurredAt := time.Time{}
		switch {
		case notInterested != nil && (!hasReaction || !reaction.StateChangedAt.After(notInterested.OccurredAt)):
			signalType, occurredAt = "not_interested", notInterested.OccurredAt
		case notInterested != nil && hasReaction && reaction.StateChangedAt.After(notInterested.OccurredAt) && reaction.Liked:
			signalType, occurredAt = "like", reaction.StateChangedAt
		case notInterested == nil && hasReaction && reaction.Liked:
			signalType, occurredAt = "like", reaction.StateChangedAt
		default:
			signalType, occurredAt = resolveRecommendationPassiveOutcome(state, views[articleID])
		}
		if signalType != "" {
			outcomes = append(outcomes, userArticleOutcome{ArticleID: articleID, SignalType: signalType, OccurredAt: occurredAt})
		}
	}
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].ArticleID < outcomes[j].ArticleID })
	return outcomes
}

func normalizedEmbeddingRecommendationConfig() config.RecommendationConfig {
	cfg := config.RecommendationConfig{
		BehaviorWeights:       config.RecommendationBehaviorWeights{View: 0.5, Like: 6, Click: 1.5, QualifiedRead: 3, QuickBounce: -3, NotInterested: -6},
		SignalHalfLifeDays:    14,
		FeedbackLookbackDays:  90,
		SemanticWeight:        4,
		FreshnessWeight:       2,
		PopularityWeight:      0.5,
		FreshnessHalfLifeDays: 2,
	}
	if config.AppConfig == nil {
		return cfg
	}
	set := config.AppConfig.Recommendation
	if set.BehaviorWeights.View != 0 {
		cfg.BehaviorWeights.View = set.BehaviorWeights.View
	}
	if set.BehaviorWeights.Like != 0 {
		cfg.BehaviorWeights.Like = set.BehaviorWeights.Like
	}
	if set.BehaviorWeights.Click != 0 {
		cfg.BehaviorWeights.Click = set.BehaviorWeights.Click
	}
	if set.BehaviorWeights.QualifiedRead != 0 {
		cfg.BehaviorWeights.QualifiedRead = set.BehaviorWeights.QualifiedRead
	}
	if set.BehaviorWeights.QuickBounce != 0 {
		cfg.BehaviorWeights.QuickBounce = set.BehaviorWeights.QuickBounce
	}
	if set.BehaviorWeights.NotInterested != 0 {
		cfg.BehaviorWeights.NotInterested = set.BehaviorWeights.NotInterested
	}
	if set.SignalHalfLifeDays > 0 {
		cfg.SignalHalfLifeDays = set.SignalHalfLifeDays
	}
	if set.FeedbackLookbackDays > 0 {
		cfg.FeedbackLookbackDays = set.FeedbackLookbackDays
	}
	if set.SemanticWeight > 0 {
		cfg.SemanticWeight = set.SemanticWeight
	}
	if set.FreshnessWeight > 0 {
		cfg.FreshnessWeight = set.FreshnessWeight
	}
	if set.PopularityWeight > 0 {
		cfg.PopularityWeight = set.PopularityWeight
	}
	if set.FreshnessHalfLifeDays > 0 {
		cfg.FreshnessHalfLifeDays = set.FreshnessHalfLifeDays
	}
	return cfg
}

var loadRecommendationArticleEmbeddings = func(articleIDs []uint, version string) (map[uint][]float32, error) {
	result := make(map[uint][]float32)
	if len(articleIDs) == 0 {
		return result, nil
	}
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var rows []models.ArticleEmbedding
	if err := global.Db.Select("article_id, embedding").Where("article_id IN ? AND version = ?", articleIDs, version).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ArticleID] = append([]float32(nil), row.Embedding.Slice()...)
	}
	return result, nil
}

func buildEmbeddingInterestProfile(behaviors []articleBehaviorSignal, feedback []recommendationFeedbackSignal, reactions map[uint]recommendationReactionState, now time.Time, cfg config.RecommendationConfig) (userInterestProfile, error) {
	profile := userInterestProfile{InteractedArticleIDs: make(map[uint]struct{})}
	for articleID := range reactions {
		if articleID != 0 {
			profile.InteractedArticleIDs[articleID] = struct{}{}
		}
	}
	outcomes := canonicalizeRecommendationOutcomes(behaviors, feedback, reactions)
	ids := make([]uint, 0, len(outcomes))
	for _, outcome := range outcomes {
		profile.InteractedArticleIDs[outcome.ArticleID] = struct{}{}
		ids = append(ids, outcome.ArticleID)
	}
	embeddingsByArticle, err := loadRecommendationArticleEmbeddings(ids, config.ActiveEmbeddingVersion())
	if err != nil {
		return profile, err
	}
	for _, outcome := range outcomes {
		weight := embeddingSignalWeight(cfg, outcome.SignalType)
		vector := embeddingsByArticle[outcome.ArticleID]
		if weight == 0 || !validEmbeddingVector(vector) {
			continue
		}
		if len(profile.Vector) == 0 {
			profile.Vector = make([]float32, len(vector))
		}
		if len(profile.Vector) != len(vector) {
			continue
		}
		effective := float32(weight * recommendationSignalDecay(now, outcome.OccurredAt, cfg.SignalHalfLifeDays))
		for index, value := range vector {
			profile.Vector[index] += value * effective
		}
		profile.PersonalizedSignalCount++
	}
	profile.Vector = normalizeEmbedding(profile.Vector)
	return profile, nil
}

func validEmbeddingVector(vector []float32) bool {
	if len(vector) == 0 {
		return false
	}
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func normalizeEmbedding(vector []float32) []float32 {
	if !validEmbeddingVector(vector) {
		return nil
	}
	norm := 0.0
	for _, value := range vector {
		norm += float64(value) * float64(value)
	}
	if norm <= 0 {
		return nil
	}
	length := float32(math.Sqrt(norm))
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = value / length
	}
	return result
}

func embeddingSignalWeight(cfg config.RecommendationConfig, signal string) float64 {
	switch signal {
	case "view":
		return cfg.BehaviorWeights.View
	case "click":
		return cfg.BehaviorWeights.Click
	case "qualified_read":
		return cfg.BehaviorWeights.QualifiedRead
	case "quick_bounce":
		return cfg.BehaviorWeights.QuickBounce
	case "like":
		return cfg.BehaviorWeights.Like
	case "not_interested":
		return cfg.BehaviorWeights.NotInterested
	default:
		return 0
	}
}

func recommendationSignalDecay(now, occurred time.Time, halfLifeDays float64) float64 {
	if occurred.IsZero() || occurred.After(now) || halfLifeDays <= 0 {
		return 1
	}
	age := now.Sub(occurred).Hours() / 24
	return math.Exp(-math.Ln2 * age / halfLifeDays)
}

func scopedEmbeddingCandidateQuery(query *gorm.DB, userID uint, excluded map[uint]struct{}, lookbackStart, now time.Time) *gorm.DB {
	negative := global.Db.Table("article_behaviors AS ab").
		Select("1").
		Where("ab.user_id = ? AND ab.article_id = articles.id AND ab.action = ? AND ab.last_seen_at >= ?", userID, eventing.RecommendationBehaviorActionNotInterested, lookbackStart)
	laterReaction := global.Db.Table("article_reaction AS ar").
		Select("1").
		Where("ar.user_id = ? AND ar.article_id = articles.id AND ar.state_changed_at > ab.last_seen_at", userID)
	negative = negative.Where("NOT EXISTS (?)", laterReaction)
	query = publicArticleScope(query, now).Where("NOT EXISTS (?)", negative)
	if ids := articleIDList(excluded); len(ids) > 0 {
		query = query.Where("articles.id NOT IN ?", ids)
	}
	return query
}

type semanticCandidateRow struct {
	ArticleID          uint    `gorm:"column:article_id"`
	SemanticSimilarity float64 `gorm:"column:semantic_similarity"`
}

func loadSemanticEmbeddingCandidates(userID uint, profile userInterestProfile, lookbackStart, now time.Time) ([]embeddingCandidate, error) {
	if len(profile.Vector) == 0 {
		return nil, nil
	}
	queryVector := pgvector.NewVector(profile.Vector)
	query := scopedEmbeddingCandidateQuery(
		global.Db.Table("article_embeddings AS ae").
			Select("ae.article_id, 1 - (ae.embedding <=> ?) AS semantic_similarity", queryVector).
			Joins("JOIN articles ON articles.id = ae.article_id").
			Where("ae.version = ? AND ae.dimensions = ?", config.ActiveEmbeddingVersion(), len(profile.Vector)),
		userID, profile.InteractedArticleIDs, lookbackStart, now,
	)
	var rows []semanticCandidateRow
	if err := query.Order(gorm.Expr("ae.embedding <=> ? ASC", queryVector)).Limit(recommendationSemanticCandidateCap).Scan(&rows).Error; err != nil {
		return nil, err
	}
	candidates := make([]embeddingCandidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, embeddingCandidate{ArticleID: row.ArticleID, SemanticSimilarity: row.SemanticSimilarity, FromSemantic: true})
	}
	return candidates, nil
}

func loadEmbeddingSourceIDs(userID uint, excluded map[uint]struct{}, lookbackStart, now time.Time, order string, limit int) ([]uint, error) {
	if limit <= 0 {
		return nil, nil
	}
	var ids []uint
	query := scopedEmbeddingCandidateQuery(global.Db.Table("articles").Select("articles.id"), userID, excluded, lookbackStart, now)
	if err := query.Order(order).Limit(limit).Pluck("articles.id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
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
				current.FromRecent = current.FromRecent || candidate.FromRecent
				current.FromPopular = current.FromPopular || candidate.FromPopular
				if candidate.FromSemantic {
					current.SemanticSimilarity = candidate.SemanticSimilarity
				}
				continue
			}
			byID[candidate.ArticleID] = len(merged)
			merged = append(merged, candidate)
			if len(merged) == limit {
				return merged
			}
		}
	}
	return merged
}

var loadEmbeddingFeedCandidates = loadEmbeddingFeedCandidatesFromDB

func loadEmbeddingFeedCandidatesFromDB(userID uint, profile userInterestProfile, lookbackStart, now time.Time) ([]embeddingCandidate, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	if len(profile.Vector) == 0 {
		recentIDs, err := loadEmbeddingSourceIDs(userID, profile.InteractedArticleIDs, lookbackStart, now, "articles.created_at DESC, articles.id DESC", recommendationColdStartRecentCap)
		if err != nil {
			return nil, err
		}
		popularIDs, err := loadEmbeddingSourceIDs(userID, profile.InteractedArticleIDs, lookbackStart, now, "articles.like_count DESC, articles.created_at DESC, articles.id DESC", recommendationColdStartPopularCap)
		if err != nil {
			return nil, err
		}
		recent := make([]embeddingCandidate, 0, len(recentIDs))
		for _, id := range recentIDs {
			recent = append(recent, embeddingCandidate{ArticleID: id, FromRecent: true})
		}
		popular := make([]embeddingCandidate, 0, len(popularIDs))
		for _, id := range popularIDs {
			popular = append(popular, embeddingCandidate{ArticleID: id, FromPopular: true})
		}
		return mergeEmbeddingCandidates(recommendationMergedCandidateCap, recent, popular), nil
	}
	semantic, err := loadSemanticEmbeddingCandidates(userID, profile, lookbackStart, now)
	if err != nil {
		return nil, err
	}
	recentIDs, err := loadEmbeddingSourceIDs(userID, profile.InteractedArticleIDs, lookbackStart, now, "articles.created_at DESC, articles.id DESC", recommendationRecentCandidateCap)
	if err != nil {
		return nil, err
	}
	popularIDs, err := loadEmbeddingSourceIDs(userID, profile.InteractedArticleIDs, lookbackStart, now, "articles.like_count DESC, articles.created_at DESC, articles.id DESC", recommendationPopularCandidateCap)
	if err != nil {
		return nil, err
	}
	recent := make([]embeddingCandidate, 0, len(recentIDs))
	for _, id := range recentIDs {
		recent = append(recent, embeddingCandidate{ArticleID: id, FromRecent: true})
	}
	popular := make([]embeddingCandidate, 0, len(popularIDs))
	for _, id := range popularIDs {
		popular = append(popular, embeddingCandidate{ArticleID: id, FromPopular: true})
	}
	return mergeEmbeddingCandidates(recommendationMergedCandidateCap, semantic, recent, popular), nil
}

func hydrateEmbeddingCandidates(candidates []embeddingCandidate, now time.Time) ([]models.Article, error) {
	ids := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ArticleID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	query := publicArticleScope(global.Db.Model(&models.Article{}).Select(publicArticleSelectColumns).Where("articles.id IN ?", ids), now)
	var articles []models.Article
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
	ordered := make([]models.Article, 0, len(candidates))
	for _, candidate := range candidates {
		if article, ok := byID[candidate.ArticleID]; ok {
			ordered = append(ordered, article)
		}
	}
	return ordered, nil
}

func rankEmbeddingCandidates(profile userInterestProfile, candidates []embeddingCandidate, articles []models.Article, now time.Time, cfg config.RecommendationConfig, limit int) []recommendedArticleResponse {
	candidateByID := make(map[uint]embeddingCandidate, len(candidates))
	for _, candidate := range candidates {
		candidateByID[candidate.ArticleID] = candidate
	}
	result := make([]recommendedArticleResponse, 0, len(articles))
	for _, article := range articles {
		if _, interacted := profile.InteractedArticleIDs[article.ID]; interacted {
			continue
		}
		candidate := candidateByID[article.ID]
		result = append(result, recommendedArticleResponse{
			ID: article.ID, Title: article.Title, Content: article.Content, Preview: article.Preview,
			CoverImageURL: article.CoverImageURL, LikeCount: article.LikeCount, CommentCount: article.CommentCount,
			ViewCount: article.ViewCount, CreatedAt: article.CreatedAt, Author: publicAuthorFromUser(article.Author),
			Score: scoreEmbeddingCandidate(candidate, article, now, cfg),
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
	if limit <= 0 || limit > maxRecommendationLimit {
		limit = defaultRecommendationLimit
	}
	if len(result) > limit {
		return result[:limit]
	}
	return result
}

func scoreEmbeddingCandidate(candidate embeddingCandidate, article models.Article, now time.Time, cfg config.RecommendationConfig) float64 {
	semantic := 0.0
	if candidate.FromSemantic {
		semantic = candidate.SemanticSimilarity
	}
	ageDays := now.Sub(article.CreatedAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	freshness := math.Exp(-math.Ln2 * ageDays / cfg.FreshnessHalfLifeDays)
	popularity := math.Log1p(math.Max(0, float64(article.LikeCount)))
	return cfg.SemanticWeight*semantic + cfg.FreshnessWeight*freshness + cfg.PopularityWeight*popularity
}

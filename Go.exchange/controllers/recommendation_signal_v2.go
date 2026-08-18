package controllers

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/models"
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

type userArticleSignal struct {
	SignalType string
	OccurredAt time.Time
}

type userArticleOutcome struct {
	ArticleID       uint
	PositiveSignals []userArticleSignal
	NegativeSignal  *userArticleSignal
	PassiveSignal   *userArticleSignal

	// SignalType and OccurredAt retain the compact representation used by the
	// old unit seam; the V2 runtime reads the structured fields above.
	SignalType string
	OccurredAt time.Time
}

const (
	recommendationFeedbackEventTypeClick         = models.RecommendationEventTypeClick
	recommendationFeedbackEventTypeReadEnd       = models.RecommendationEventTypeReadEnd
	recommendationFeedbackEventTypeNotInterested = models.RecommendationEventTypeNotInterested
)

var loadRecommendationBehaviorSignals = func(userID uint) ([]articleBehaviorSignal, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	var views []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND action = ?", userID, ArticleBehaviorActionView).
		Order("last_seen_at DESC, id DESC").Limit(recommendationRecentViewArticleLimit).Find(&views).Error; err != nil {
		return nil, err
	}
	var replies []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND action = ?", userID, ArticleBehaviorActionReply).
		Order("last_seen_at DESC, id DESC").Limit(recommendationFeedbackArticleLimit).Find(&replies).Error; err != nil {
		return nil, err
	}
	result := make([]articleBehaviorSignal, 0, len(views)+len(replies))
	for _, behavior := range views {
		if behavior.ArticleID != 0 {
			result = append(result, articleBehaviorSignal{Behavior: behavior})
		}
	}
	for _, behavior := range replies {
		if behavior.ArticleID != 0 {
			result = append(result, articleBehaviorSignal{Behavior: behavior})
		}
	}
	return result, nil
}

var loadRecommendationFeedbackSignals = func(userID uint, lookbackStart time.Time) ([]recommendationFeedbackSignal, error) {
	if global.Db == nil {
		return nil, errors.New("database is not initialized")
	}
	actions := []string{
		eventing.RecommendationBehaviorActionClick,
		eventing.RecommendationBehaviorActionReadQualified,
		eventing.RecommendationBehaviorActionReadQuickBounce,
		eventing.RecommendationBehaviorActionReadNeutral,
	}
	var behaviors []models.ArticleBehavior
	if err := global.Db.Where("user_id = ? AND ((action IN ? AND last_seen_at >= ?) OR action = ?)",
		userID, actions, lookbackStart, eventing.RecommendationBehaviorActionNotInterested).
		Order("last_seen_at DESC, id DESC").Find(&behaviors).Error; err != nil {
		return nil, err
	}
	result := make([]recommendationFeedbackSignal, 0, len(behaviors))
	for _, behavior := range behaviors {
		if behavior.ArticleID == 0 {
			continue
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
			event.EventType, signal.SignalType = recommendationFeedbackEventTypeClick, "click"
		case eventing.RecommendationBehaviorActionReadQualified,
			eventing.RecommendationBehaviorActionReadQuickBounce,
			eventing.RecommendationBehaviorActionReadNeutral:
			event.EventType = recommendationFeedbackEventTypeReadEnd
			outcome := recommendationReadOutcomeNeutral
			switch behavior.Action {
			case eventing.RecommendationBehaviorActionReadQualified:
				outcome, signal.SignalType = recommendationReadOutcomeQualified, "qualified_read"
			case eventing.RecommendationBehaviorActionReadQuickBounce:
				outcome, signal.SignalType = recommendationReadOutcomeQuickBounce, "quick_bounce"
			default:
				signal.SignalType = "neutral_read"
			}
			event.ReadOutcome = &outcome
		case eventing.RecommendationBehaviorActionNotInterested:
			event.EventType, signal.SignalType = recommendationFeedbackEventTypeNotInterested, "not_interested"
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
		if reaction.ArticleID != 0 {
			states[reaction.ArticleID] = recommendationReactionState{Liked: reaction.Liked, StateChangedAt: reaction.StateChangedAt}
		}
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
		click, readEnd = state.Click, state.ReadEnd
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
	replies := make(map[uint]models.ArticleBehavior)
	feedbackByArticle := make(map[uint]*recommendationArticleFeedbackState)
	articleIDs := make(map[uint]struct{})
	for _, item := range behaviors {
		articleID := item.Behavior.ArticleID
		if articleID == 0 {
			continue
		}
		articleIDs[articleID] = struct{}{}
		switch item.Behavior.Action {
		case ArticleBehaviorActionView:
			current, exists := views[articleID]
			if !exists || item.Behavior.LastSeenAt.After(current.LastSeenAt) ||
				(item.Behavior.LastSeenAt.Equal(current.LastSeenAt) && item.Behavior.ID > current.ID) {
				views[articleID] = item.Behavior
			}
		case ArticleBehaviorActionReply:
			current, exists := replies[articleID]
			if !exists || item.Behavior.LastSeenAt.After(current.LastSeenAt) ||
				(item.Behavior.LastSeenAt.Equal(current.LastSeenAt) && item.Behavior.ID > current.ID) {
				replies[articleID] = item.Behavior
			}
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
		positive := make([]userArticleSignal, 0, 2)
		if hasReaction && reaction.Liked && (notInterested == nil || reaction.StateChangedAt.After(notInterested.OccurredAt)) {
			positive = append(positive, userArticleSignal{SignalType: "like", OccurredAt: reaction.StateChangedAt})
		}
		if reply, ok := replies[articleID]; ok && (notInterested == nil || reply.LastSeenAt.After(notInterested.OccurredAt)) {
			positive = append(positive, userArticleSignal{SignalType: "reply", OccurredAt: reply.LastSeenAt})
		}
		passiveType, passiveAt := resolveRecommendationPassiveOutcome(state, views[articleID])
		outcome := userArticleOutcome{ArticleID: articleID}
		if passiveType != "" {
			outcome.PassiveSignal = &userArticleSignal{SignalType: passiveType, OccurredAt: passiveAt}
		}
		switch {
		case len(positive) > 0:
			outcome.PositiveSignals = positive
			outcome.SignalType, outcome.OccurredAt = positive[0].SignalType, positive[0].OccurredAt
		case notInterested != nil:
			signal := userArticleSignal{SignalType: "not_interested", OccurredAt: notInterested.OccurredAt}
			outcome.NegativeSignal = &signal
			outcome.SignalType, outcome.OccurredAt = signal.SignalType, signal.OccurredAt
		case passiveType == "quick_bounce":
			signal := userArticleSignal{SignalType: passiveType, OccurredAt: passiveAt}
			outcome.NegativeSignal = &signal
			outcome.SignalType, outcome.OccurredAt = signal.SignalType, signal.OccurredAt
		case passiveType != "":
			outcome.SignalType, outcome.OccurredAt = passiveType, passiveAt
		}
		if len(outcome.PositiveSignals) > 0 || outcome.NegativeSignal != nil || outcome.PassiveSignal != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	sort.Slice(outcomes, func(i, j int) bool { return outcomes[i].ArticleID < outcomes[j].ArticleID })
	return outcomes
}
